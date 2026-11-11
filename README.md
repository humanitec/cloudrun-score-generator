# cloudrun-score-generator

A Go library that turns a [Score](https://score.dev) workload into a
[Google Cloud Run](https://cloud.google.com/run) service manifest
(`serving.knative.dev/v1` `Service`). This manifest can be applied with `gcloud run services replace`.

Secret values are never written into the manifest. They are stored in Google Secret Manager,
and the manifest references them.

`cloudrun.Generator` implements the `score.Generator` interface from
[`github.com/score-spec/score-go/v2/score`](https://github.com/score-spec/score-go/tree/v2), so it
can be combined with any `score.Normalizer` and `score.Provisioner`.

## Installation

As a library:

```sh
go get github.com/humanitec/cloudrun-score-generator@latest
```

As a command line tool:

```sh
go install github.com/humanitec/cloudrun-score-generator/cmd/score-cloudrun@latest
```

## Packages

| Import path | Package | Contents |
|---|---|---|
| `github.com/humanitec/cloudrun-score-generator` | `cloudrun` | `Generator`, which implements `score.Generator` for Cloud Run |
| `github.com/humanitec/cloudrun-score-generator/secretmanager` | `secretmanager` | `Client`, a `cloudrun.SecretSaver` backed by Google Secret Manager |

The root package is named `cloudrun`, not after the module path, so you refer to it as `cloudrun.Generator`.

## Usage

Given a Score file:

```yaml
apiVersion: score.dev/v1b1
metadata:
  name: hello-world
containers:
  main:
    image: ghcr.io/example/hello-world:latest
    variables:
      DB_HOST: ${resources.db.host}
      DB_PASSWORD: ${resources.db.password}
service:
  ports:
    http1:
      port: 8080
resources:
  db:
    type: postgres
```

load it, provide substitutions for its `${resources.*}` placeholders, and generate the manifest:

```go
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/score-spec/score-go/v2/score"
	"sigs.k8s.io/yaml"

	cloudrun "github.com/humanitec/cloudrun-score-generator"
	"github.com/humanitec/cloudrun-score-generator/secretmanager"
)

func main() {
	ctx := context.Background()

	src, err := os.ReadFile("score.yaml")
	if err != nil {
		log.Fatal(err)
	}
	var workload score.Workload
	if err := yaml.Unmarshal(src, &workload); err != nil {
		log.Fatal(err)
	}

	// Substitutions normally come from your score.Provisioner: one entry per ${resources.*} placeholder.
	substitutions := score.Substitutions{
		"resources.db.host":     {Value: "10.0.0.5"},
		"resources.db.password": {Secret: &score.SecretRef{Type: score.SecretTypeDirect, Value: "s3cr3t"}},
	}

	secrets := secretmanager.New("my-gcp-project", "europe-west1")
	defer secrets.Close()

	var gen score.Generator = cloudrun.NewGenerator(secrets)
	manifests, err := gen.Generate(ctx, &workload, substitutions)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(string(manifests[cloudrun.ServiceManifestKey]))
}
```

`Generate` returns a map with one entry, `cloudrun.ServiceManifestKey`, which holds the service
manifest as JSON. In this example `DB_HOST` is set as a plain value. `DB_PASSWORD` is saved to
Secret Manager and referenced from the manifest through `secretKeyRef`.

### Substitutions

Each key in `score.Substitutions` is a placeholder expression without the surrounding `${}`, for
example `resources.db.host`. Only `resources.*` placeholders are resolved. Replace any other
placeholders, such as `${metadata.name}`, before calling `Generate` (it should be a part of Normalization).

A `score.SubValue` holds either:

- `Value`: a plain value. Values that aren't strings are encoded as JSON.
- `Secret`: a secret. Only `score.SecretTypeDirect` is supported for now, with the secret value in
  `SecretRef.Value`.

A variable that uses at least one secret placeholder is stored in Secret Manager as a whole and
referenced from the manifest. Other variables are set as plain values. Files are always stored in
Secret Manager, so a file must use at least one secret placeholder (see [Limitations](#limitations)).

### Service name

The service is named after `metadata.name`, unless the workload has the
`google-cloud-run.score.dev/service-name` annotation (`cloudrun.ServiceNameAnnotation`). Use
`cloudrun.WithServiceName` to set it, for example to deploy one workload to several environments:

```go
workload = cloudrun.WithServiceName(workload, "hello-world-dev")
```

`cloudrun.ServiceName(&workload)` returns the name the generator will use.

### Secrets

`NewGenerator` takes a `cloudrun.SecretSaver`. `secretmanager.Client` is the default
implementation. You can provide your own, for example for tests:

```go
type SecretSaver interface {
	// SaveSecret stores value under name for the service and returns the secret's version.
	SaveSecret(ctx context.Context, service, name, value string) (string, error)
}
```

`secretmanager.New(project, region, ...Option)`:

- creates secrets in `project`, replicated to `region`. Cloud Run only accepts secrets replicated
  to the service's region, so `region` must be the region the service runs in.
- adds a version only when the value has changed, so redeploying an unchanged workload doesn't
  create a new revision.
- labels each secret with `managed-by` and `service`.
- connects to Google only when the first secret is saved. A workload without secrets needs
  neither credentials nor a project.

Options:

```go
secrets := secretmanager.New("my-gcp-project", "europe-west1",
	// Credentials, endpoint or quota project for the Google client.
	// Default: Application Default Credentials.
	secretmanager.WithClientOptions(option.WithCredentialsFile("key.json")),
	// Value of the managed-by label. Default: secretmanager.DefaultManagedBy ("score2cloudrun").
	secretmanager.WithManagedBy("my-platform"),
)
```

To remove secrets that earlier deployments of a service created but the current one no longer
uses, call `DeleteUnused` with the secrets the deployed manifest references. Call it only after
the manifest has been deployed: until then the running revision still reads the old secrets.

```go
manifest := manifests[cloudrun.ServiceManifestKey]
// ... deploy the manifest ...
keep, err := cloudrun.ReferencedSecrets(manifest)
if err != nil {
	log.Fatal(err)
}
if err := secrets.DeleteUnused(ctx, cloudrun.ServiceName(&workload), keep); err != nil {
	log.Fatal(err)
}
```

`DeleteUnused` deletes secrets labeled with `managed-by=<WithManagedBy value, default score2cloudrun>` and `service=<service-name>`
except those listed in `keep`. Pass empty `keep` to delete all secrets after the service has been
deleted.

### Volumes

A volume source must be `${resources.NAME}`, where `NAME` is a resource of type `volume`. Its
substitution is the whole resource: a value with a `google-cloud-run` key holding a Kubernetes
volume spec. For example, to mount a Cloud Storage bucket:

```go
substitutions["resources.data"] = score.SubValue{Value: map[string]any{
	"google-cloud-run": map[string]any{
		"csi": map[string]any{
			"driver":           "gcsfuse.run.googleapis.com",
			"volumeAttributes": map[string]any{"bucketName": "my-bucket"},
		},
	},
}}
```

## Limitations

Cloud Run limitations:

- **Ports:** at most one port. If the workload has a `service`, it can have only one container.
  Name the port `http1` or `h2c` to choose the protocol.
- **Files:** file content can only be mounted from a Secret Manager secret, and only one file per
  directory ([details](https://cloud.google.com/run/docs/configuring/services/secrets#limitations)).

## Command line tool

`score-cloudrun generate` prints the service manifest as YAML, or as JSON with `--output json`.
The workload and the substitutions are read from `.yaml`, `.yml` or `.json` files. The format is
chosen by the file extension. The substitutions file maps placeholders to values, in the format
of `score.Substitutions`:

```yaml
resources.db.host:
  value: 10.0.0.5
resources.db.password:
  secret:
    type: direct
    value: s3cr3t
```

The tool runs no normalizer or provisioner. Only `${resources.*}` placeholders are resolved.

By default, `generate` is a dry run. Secrets are not saved, and the manifest references their
`latest` version, which is useful to preview the manifest without Google credentials. To deploy
the manifest, pass `--save-secrets` so the secrets are stored in Secret Manager:

```sh
score-cloudrun generate --file score.yaml --substitutions subs.yaml --service-name hello-world-dev \
  --save-secrets --project my-gcp-project --region europe-west1 > service.yaml
gcloud run services replace service.yaml --region europe-west1
score-cloudrun delete-unused-secrets --manifest service.yaml --project my-gcp-project
```

`delete-unused-secrets` deletes the service's secrets that the deployed manifest no longer
references. Run it only after the deployment succeeded. To delete all secrets of a service, for
example after deleting the service:

```sh
gcloud run services delete hello-world-dev --region europe-west1
score-cloudrun delete-unused-secrets --service hello-world-dev --all --project my-gcp-project
```

Run `score-cloudrun --help` or `score-cloudrun COMMAND --help` for all flags. The tool exits
with code 2 for usage errors and 1 for other errors.
