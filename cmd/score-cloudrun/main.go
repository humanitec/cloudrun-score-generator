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

// Command score-cloudrun generates Google Cloud Run service manifests from Score
// workloads and cleans up the Secret Manager secrets they no longer use.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/score-spec/score-go/v2/score"
	"sigs.k8s.io/yaml"

	cloudrun "github.com/humanitec/cloudrun-score-generator"
	"github.com/humanitec/cloudrun-score-generator/secretmanager"
)

const usage = `score-cloudrun generates Google Cloud Run service manifests from Score workloads.

Usage:
  score-cloudrun generate --file score.yaml [--substitutions subs.yaml] [--service-name NAME] [--output yaml|json]
                          [--save-secrets --project PROJECT --region REGION]
  score-cloudrun delete-unused-secrets --project PROJECT --manifest service.json
  score-cloudrun delete-unused-secrets --project PROJECT --service NAME --all

Commands:
  generate               Print the Cloud Run service manifest for a Score workload.
  delete-unused-secrets  Delete the secrets of a service that its deployed manifest no longer
                         references. Run it after the manifest has been deployed.

Run "score-cloudrun COMMAND --help" for the flags of a command.
`

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

// usageError is an error in how the command was called. It exits with code 2.
type usageError struct{ msg string }

func (e usageError) Error() string { return e.msg }

func usageErrorf(format string, args ...any) error {
	return usageError{msg: fmt.Sprintf(format, args...)}
}

// run runs the command line args and returns the exit code.
func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprint(stderr, usage)
		return 2
	}

	var err error
	switch args[0] {
	case "generate":
		err = runGenerate(ctx, args[1:], stdout, stderr)
	case "delete-unused-secrets":
		err = runDeleteUnusedSecrets(ctx, args[1:], stderr)
	case "help", "-h", "-help", "--help":
		_, _ = fmt.Fprint(stdout, usage)
		return 0
	default:
		err = usageErrorf("unknown command %q", args[0])
	}

	var uerr usageError
	switch {
	case err == nil:
		return 0
	case errors.Is(err, flag.ErrHelp):
		return 0
	case errors.As(err, &uerr):
		_, _ = fmt.Fprintf(stderr, "score-cloudrun: %v\nRun \"score-cloudrun --help\" for usage.\n", err)
		return 2
	default:
		_, _ = fmt.Fprintf(stderr, "score-cloudrun: %v\n", err)
		return 1
	}
}

// newFlagSet returns a flag set for the command that reports parse errors as usage errors.
func newFlagSet(name, synopsis string, stderr io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		_, _ = fmt.Fprintf(stderr, "Usage: score-cloudrun %s\n\n%s\n\nFlags:\n", name, synopsis)
		fs.PrintDefaults()
	}
	return fs
}

// parse parses args into fs and rejects positional arguments.
func parse(fs *flag.FlagSet, args []string) error {
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return err
		}
		return usageError{msg: err.Error()}
	}
	if fs.NArg() > 0 {
		return usageErrorf("%s: unexpected arguments: %s", fs.Name(), strings.Join(fs.Args(), " "))
	}
	return nil
}

func runGenerate(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := newFlagSet("generate", `Generates the Cloud Run service manifest for a Score workload, as YAML or JSON.

Only ${resources.*} placeholders are resolved. The Score specification must be normalized.

By default secrets are not saved and the manifest references their "latest" version, which is
enough to preview the manifest. Pass --save-secrets to store them in Google Secret Manager,
which is required for a manifest that is deployed.`, stderr)
	file := fs.String("file", "", "Score workload file, .yaml, .yml or .json (required)")
	subsFile := fs.String("substitutions", "", "substitutions file, .yaml, .yml or .json: a map of placeholders, such as resources.db.host, to {value: ...} or {secret: {type: direct, value: ...}}")
	serviceName := fs.String("service-name", "", "Cloud Run service name, instead of the workload's annotation or metadata.name")
	saveSecrets := fs.Bool("save-secrets", false, "store secrets in Google Secret Manager (requires --project and --region)")
	project := fs.String("project", "", "Google Cloud project to store the secrets in")
	region := fs.String("region", "", "region the service runs in; secrets are replicated to it")
	output := fs.String("output", "yaml", "output format: yaml or json")
	if err := parse(fs, args); err != nil {
		return err
	}
	if *file == "" {
		return usageErrorf("generate: --file is required")
	}
	if *output != "yaml" && *output != "json" {
		return usageErrorf("generate: --output must be yaml or json, got %q", *output)
	}
	if *saveSecrets && (*project == "" || *region == "") {
		return usageErrorf("generate: --save-secrets requires --project and --region")
	}
	if !*saveSecrets && (*project != "" || *region != "") {
		return usageErrorf("generate: --project and --region are only used with --save-secrets")
	}

	var workload score.Workload
	if err := readFile(*file, &workload); err != nil {
		return err
	}
	substitutions := score.Substitutions{}
	if *subsFile != "" {
		if err := readFile(*subsFile, &substitutions); err != nil {
			return err
		}
	}
	if *serviceName != "" {
		workload = cloudrun.WithServiceName(workload, *serviceName)
	}

	var saver cloudrun.SecretSaver
	dryRun := &dryRunSaver{}
	if *saveSecrets {
		secrets := secretmanager.New(*project, *region)
		defer func() {
			if err := secrets.Close(); err != nil {
				_, _ = fmt.Fprintf(stderr, "score-cloudrun: closing the secret manager client: %v\n", err)
			}
		}()
		saver = secrets
	} else {
		saver = dryRun
	}

	manifests, err := cloudrun.NewGenerator(saver).Generate(ctx, &workload, substitutions)
	if err != nil {
		return err
	}
	manifest := manifests[cloudrun.ServiceManifestKey]
	if *output == "yaml" {
		if manifest, err = yaml.JSONToYAML(manifest); err != nil {
			return fmt.Errorf("converting the manifest to yaml: %w", err)
		}
	} else {
		manifest = append(manifest, '\n')
	}
	if _, err := stdout.Write(manifest); err != nil {
		return fmt.Errorf("writing the manifest: %w", err)
	}
	if len(dryRun.names) > 0 {
		_, _ = fmt.Fprintf(stderr, "score-cloudrun: %d secret(s) not saved; pass --save-secrets before deploying this manifest\n", len(dryRun.names))
	}
	return nil
}

// dryRunSaver saves nothing. It references the latest version of each secret,
// so the manifest is complete but deterministic and needs no Google credentials.
type dryRunSaver struct {
	names []string
}

func (d *dryRunSaver) SaveSecret(_ context.Context, _, name, _ string) (string, error) {
	d.names = append(d.names, name)
	return "latest", nil
}

func runDeleteUnusedSecrets(ctx context.Context, args []string, stderr io.Writer) error {
	fs := newFlagSet("delete-unused-secrets", `Deletes the Google Secret Manager secrets, related to the service, but not referenced by its manifest anymore.
May be used to clean unused secrets after a new version of the service manifest has been deployed.

To delete all secrets of a service, for example after deleting the service, pass
--service and --all instead of --manifest.`, stderr)
	manifestFile := fs.String("manifest", "", "deployed service manifest, .yaml, .yml or .json; its secrets are kept")
	service := fs.String("service", "", "Cloud Run service name, with --all")
	all := fs.Bool("all", false, "delete all secrets of --service")
	project := fs.String("project", "", "Google Cloud project the secrets are in (required)")
	if err := parse(fs, args); err != nil {
		return err
	}
	if *project == "" {
		return usageErrorf("delete-unused-secrets: --project is required")
	}
	switch {
	case *manifestFile != "" && (*all || *service != ""):
		return usageErrorf("delete-unused-secrets: --manifest cannot be combined with --service or --all")
	case *manifestFile == "" && !*all:
		return usageErrorf("delete-unused-secrets: pass --manifest, or --service and --all")
	case *all && *service == "":
		return usageErrorf("delete-unused-secrets: --all requires --service")
	}

	var keep []string
	if *manifestFile != "" {
		var err error
		if *service, keep, err = readManifest(*manifestFile); err != nil {
			return err
		}
	}

	secrets := secretmanager.New(*project, "")
	defer func() {
		if err := secrets.Close(); err != nil {
			_, _ = fmt.Fprintf(stderr, "score-cloudrun: closing the secret manager client: %v\n", err)
		}
	}()
	return secrets.DeleteUnused(ctx, *service, keep)
}

// readManifest returns the service name and the secrets referenced by the service manifest at path.
func readManifest(path string) (string, []string, error) {
	var manifest struct {
		Metadata struct {
			Name string `json:"name"`
		} `json:"metadata"`
	}
	if err := readFile(path, &manifest); err != nil {
		return "", nil, err
	}
	if manifest.Metadata.Name == "" {
		return "", nil, fmt.Errorf("%s: the manifest has no metadata.name", path)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", nil, err
	}
	keep, err := cloudrun.ReferencedSecrets(raw)
	if err != nil {
		return "", nil, fmt.Errorf("%s: %w", path, err)
	}
	return manifest.Metadata.Name, keep, nil
}

// readFile decodes the file at path into out, as JSON or YAML depending on its extension.
func readFile(path string, out any) error {
	ext := strings.ToLower(filepath.Ext(path))
	var unmarshal func([]byte, any) error
	switch ext {
	case ".json":
		unmarshal = json.Unmarshal
	case ".yaml", ".yml":
		unmarshal = func(b []byte, v any) error { return yaml.Unmarshal(b, v) }
	default:
		return usageErrorf("%s: unsupported file extension %q, expected .yaml, .yml or .json", path, ext)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := unmarshal(raw, out); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}
