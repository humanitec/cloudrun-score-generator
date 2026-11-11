// Copyright 2026 Ternki GmbH
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package secretmanager stores the secrets of Cloud Run services in Google Secret Manager.
// Its Client implements cloudrun.SecretSaver.
package secretmanager

import (
	"context"
	"errors"
	"fmt"
	"path"
	"slices"

	gsm "cloud.google.com/go/secretmanager/apiv1"
	"cloud.google.com/go/secretmanager/apiv1/secretmanagerpb"
	gax "github.com/googleapis/gax-go/v2"
	"google.golang.org/api/option"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	managedByLabel = "managed-by"
	serviceLabel   = "service"

	// DefaultManagedBy is the value of the managed-by label on the secrets a Client creates,
	// unless WithManagedBy sets another one.
	DefaultManagedBy = "score2cloudrun"
)

type api interface {
	CreateSecret(context.Context, *secretmanagerpb.CreateSecretRequest, ...gax.CallOption) (*secretmanagerpb.Secret, error)
	AddSecretVersion(context.Context, *secretmanagerpb.AddSecretVersionRequest, ...gax.CallOption) (*secretmanagerpb.SecretVersion, error)
	AccessSecretVersion(context.Context, *secretmanagerpb.AccessSecretVersionRequest, ...gax.CallOption) (*secretmanagerpb.AccessSecretVersionResponse, error)
	ListSecrets(context.Context, *secretmanagerpb.ListSecretsRequest, ...gax.CallOption) ([]*secretmanagerpb.Secret, error)
	DeleteSecret(context.Context, *secretmanagerpb.DeleteSecretRequest, ...gax.CallOption) error
	Close() error
}

// gsmAPI is an adapter to api to enable testing - ListSecrets return an iterator, which is not an interface and
// can't be mocked.
type gsmAPI struct {
	*gsm.Client
}

func (g gsmAPI) ListSecrets(ctx context.Context, req *secretmanagerpb.ListSecretsRequest, opts ...gax.CallOption) ([]*secretmanagerpb.Secret, error) {
	var secrets []*secretmanagerpb.Secret
	for secret, err := range g.Client.ListSecrets(ctx, req, opts...).All() {
		if err != nil {
			return nil, err
		}
		secrets = append(secrets, secret)
	}
	return secrets, nil
}

// Client reads and writes the secrets of Cloud Run services in Google Secret Manager.
// Each secret is labelled with the service it belongs to.
//
// The underlying Google client is built on first use, so that converting a
// workload which holds no secrets needs neither Google credentials nor a
// project and region.
type Client struct {
	project       string
	region        string
	managedBy     string
	clientOptions []option.ClientOption
	api           api
}

// Option configures a Client.
type Option func(*Client)

// WithClientOptions passes options, such as credentials or an endpoint, to the
// underlying Google Secret Manager client. Without them the client uses
// Application Default Credentials.
func WithClientOptions(opts ...option.ClientOption) Option {
	return func(c *Client) {
		c.clientOptions = append(c.clientOptions, opts...)
	}
}

// WithManagedBy sets the value of the managed-by label that marks the secrets
// the Client creates and deletes. It defaults to DefaultManagedBy. Changing it
// for existing services leaves their old secrets behind, as DeleteUnused no
// longer finds them.
func WithManagedBy(value string) Option {
	return func(c *Client) {
		c.managedBy = value
	}
}

// New returns a Client that stores secrets in the project, replicated to the
// region. Cloud Run only accepts a secret replicated to the region the service
// runs in, so region must be the service's region.
func New(project, region string, opts ...Option) *Client {
	c := &Client{
		project:   project,
		region:    region,
		managedBy: DefaultManagedBy,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// ensureClient builds the Secret Manager client on first use.
func (c *Client) ensureClient(ctx context.Context) error {
	if c.api != nil {
		return nil
	}
	if c.project == "" {
		return errors.New("no project to store the secrets in")
	}

	client, err := gsm.NewClient(ctx, c.clientOptions...)
	if err != nil {
		return fmt.Errorf("connecting to google secret manager: %w", err)
	}
	c.api = gsmAPI{client}
	return nil
}

// Close releases the Secret Manager client. It is safe to call when no client
// was ever built.
func (c *Client) Close() error {
	if c.api == nil {
		return nil
	}
	return c.api.Close()
}

// parent is the collection the project's secrets live in.
func (c *Client) parent() string {
	return "projects/" + c.project
}

// labels marks a secret as managed by this Client, for the service.
func (c *Client) labels(service string) map[string]string {
	return map[string]string{
		managedByLabel: c.managedBy,
		serviceLabel:   service,
	}
}

// filter selects the secrets this Client manages for the service, in the syntax
// of https://cloud.google.com/secret-manager/docs/filtering.
func (c *Client) filter(service string) string {
	return fmt.Sprintf("labels.%s=%q AND labels.%s=%q", managedByLabel, c.managedBy, serviceLabel, service)
}

// SaveSecret creates the service's secret if it does not exist yet and returns its version.
func (c *Client) SaveSecret(ctx context.Context, service, name, value string) (string, error) {
	if c.region == "" {
		return "", errors.New("no region to replicate the secrets to")
	}
	if err := c.ensureClient(ctx); err != nil {
		return "", err
	}
	secretPath := c.parent() + "/secrets/" + name

	// Cloud Run only accepts a user-managed secret if it is replicated in the
	// region the service runs in, so the two regions are necessarily the same.
	_, err := c.api.CreateSecret(ctx, &secretmanagerpb.CreateSecretRequest{
		Parent:   c.parent(),
		SecretId: name,
		Secret: &secretmanagerpb.Secret{
			Replication: &secretmanagerpb.Replication{
				Replication: &secretmanagerpb.Replication_UserManaged_{
					UserManaged: &secretmanagerpb.Replication_UserManaged{
						Replicas: []*secretmanagerpb.Replication_UserManaged_Replica{
							{Location: c.region},
						},
					},
				},
			},
			Labels: c.labels(service),
		},
	})
	if err != nil && status.Code(err) != codes.AlreadyExists {
		return "", fmt.Errorf("creating secret %s: %w", name, err)
	}

	// Add a new version only if the value changed. A new version would force a new Cloud Run
	// revision even when nothing changed.
	version, stored, err := c.currentVersion(ctx, secretPath)
	if err != nil {
		return "", err
	}
	if version != "" && stored == value {
		return version, nil
	}

	added, err := c.api.AddSecretVersion(ctx, &secretmanagerpb.AddSecretVersionRequest{
		Parent:  secretPath,
		Payload: &secretmanagerpb.SecretPayload{Data: []byte(value)},
	})
	if err != nil {
		return "", fmt.Errorf("adding a version to secret %s: %w", name, err)
	}
	return path.Base(added.GetName()), nil
}

// DeleteUnused deletes the secrets saved previously for the service, except
// those listed in `keep`.
//
// Call this method only after the Cloud Run Service deployment succeeded: until
// then the running revision still uses the old secrets. With an empty
// keep, all service's secrets are deleted, as when the service is destroyed.
//
// Failures are joined - one secret that won't delete does not stop the rest.
func (c *Client) DeleteUnused(ctx context.Context, service string, keep []string) error {
	if err := c.ensureClient(ctx); err != nil {
		return err
	}

	secrets, err := c.api.ListSecrets(ctx, &secretmanagerpb.ListSecretsRequest{
		Parent: c.parent(),
		Filter: c.filter(service),
	})
	if err != nil {
		return fmt.Errorf("listing the secrets of service %s: %w", service, err)
	}

	var errs []error
	for _, secret := range secrets {
		name := path.Base(secret.GetName())
		if slices.Contains(keep, name) {
			continue
		}
		err := c.api.DeleteSecret(ctx, &secretmanagerpb.DeleteSecretRequest{Name: secret.GetName()})
		if err != nil && status.Code(err) != codes.NotFound {
			errs = append(errs, fmt.Errorf("deleting secret %s: %w", name, err))
		}
	}
	return errors.Join(errs...)
}

// currentVersion returns the newest version of the secret at secretPath and the value that version holds.
func (c *Client) currentVersion(ctx context.Context, secretPath string) (string, string, error) {
	current, err := c.api.AccessSecretVersion(ctx, &secretmanagerpb.AccessSecretVersionRequest{
		Name: secretPath + "/versions/latest",
	})
	switch status.Code(err) {
	case codes.OK:
	case codes.NotFound, codes.FailedPrecondition:
		// The secret may have just been created and have no versions, or its newest
		// version may have been disabled or destroyed.
		return "", "", nil
	default:
		return "", "", fmt.Errorf("reading the newest version of secret %s: %w", path.Base(secretPath), err)
	}
	return path.Base(current.GetName()), string(current.GetPayload().GetData()), nil
}
