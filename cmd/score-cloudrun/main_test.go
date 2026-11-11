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

package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	servingv1 "knative.dev/serving/pkg/apis/serving/v1"
	"sigs.k8s.io/yaml"
)

const workloadYAML = `apiVersion: score.dev/v1b1
metadata:
  name: hello-world
containers:
  main:
    image: busybox:latest
    variables:
      DB_HOST: ${resources.db.host}
      DB_PASSWORD: ${resources.db.password}
resources:
  db:
    type: postgres
`

const substitutionsYAML = `resources.db.host:
  value: 10.0.0.5
resources.db.password:
  secret:
    type: direct
    value: s3cr3t
`

// writeFile writes content in a temporary directory, converting it to
// JSON in case of a .json extension, and returns its path.
func writeFile(t *testing.T, name, content string) string {
	t.Helper()
	data := []byte(content)
	if filepath.Ext(name) == ".json" {
		var err error
		data, err = yaml.YAMLToJSON(data)
		require.NoError(t, err)
	}
	path := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(path, data, 0o600))
	return path
}

func runArgs(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := run(t.Context(), args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func TestGenerate(t *testing.T) {
	testCases := []struct {
		name, workloadFile, substitutionsFile string
		output                                string // --output; empty means the default
		wantJSON                              bool
	}{
		{name: "yaml by default", workloadFile: "score.yaml", substitutionsFile: "subs.yml"},
		{name: "json", workloadFile: "score.json", substitutionsFile: "subs.json", output: "json", wantJSON: true},
		{name: "mixed inputs", workloadFile: "score.YAML", substitutionsFile: "subs.json", output: "yaml"},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			args := []string{"generate",
				"--file", writeFile(t, tc.workloadFile, workloadYAML),
				"--substitutions", writeFile(t, tc.substitutionsFile, substitutionsYAML),
				"--service-name", "hello-world-dev",
			}
			if tc.output != "" {
				args = append(args, "--output", tc.output)
			}
			code, stdout, stderr := runArgs(t, args...)
			require.Equal(t, 0, code, stderr)
			assert.Equal(t, tc.wantJSON, json.Valid([]byte(stdout)), stdout)

			var service servingv1.Service
			require.NoError(t, yaml.Unmarshal([]byte(stdout), &service))
			assert.Equal(t, "hello-world-dev", service.Name)
			env := service.Spec.Template.Spec.Containers[0].Env
			require.Len(t, env, 2)
			assert.Equal(t, "10.0.0.5", env[0].Value)
			require.NotNil(t, env[1].ValueFrom)
			assert.Equal(t, "latest", env[1].ValueFrom.SecretKeyRef.Key, "a dry run references the latest version")
			assert.Contains(t, stderr, "1 secret(s) not saved")
		})
	}
}

func TestGenerateWithoutSecretsPrintsNoWarning(t *testing.T) {
	code, _, stderr := runArgs(t, "generate", "--file", writeFile(t, "score.yaml", `apiVersion: score.dev/v1b1
metadata:
  name: hello-world
containers:
  main:
    image: busybox:latest
`))
	require.Equal(t, 0, code, stderr)
	assert.Empty(t, stderr)
}

func TestUsageErrors(t *testing.T) {
	workload := writeFile(t, "score.yaml", workloadYAML)
	manifest := writeFile(t, "service.json", `metadata: {name: hello-world}`)

	testCases := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{name: "no command", args: nil, wantErr: "Usage:"},
		{name: "unknown command", args: []string{"deploy"}, wantErr: `unknown command "deploy"`},
		{name: "unknown flag", args: []string{"generate", "--nope"}, wantErr: "flag provided but not defined: -nope"},
		{name: "positional arguments", args: []string{"generate", "--file", workload, "extra"}, wantErr: "unexpected arguments: extra"},
		{name: "generate without file", args: []string{"generate"}, wantErr: "--file is required"},
		{name: "unknown output format", args: []string{"generate", "--file", workload, "--output", "toml"}, wantErr: `--output must be yaml or json, got "toml"`},
		{name: "unsupported extension", args: []string{"generate", "--file", "score.toml"}, wantErr: `unsupported file extension ".toml"`},
		{name: "save secrets without project", args: []string{"generate", "--file", workload, "--save-secrets", "--region", "europe-west1"}, wantErr: "--save-secrets requires --project and --region"},
		{name: "save secrets without region", args: []string{"generate", "--file", workload, "--save-secrets", "--project", "p"}, wantErr: "--save-secrets requires --project and --region"},
		{name: "project without save secrets", args: []string{"generate", "--file", workload, "--project", "p"}, wantErr: "only used with --save-secrets"},
		{name: "delete without project", args: []string{"delete-unused-secrets", "--manifest", manifest}, wantErr: "--project is required"},
		{name: "delete without manifest or all", args: []string{"delete-unused-secrets", "--project", "p", "--service", "s"}, wantErr: "pass --manifest, or --service and --all"},
		{name: "delete with manifest and all", args: []string{"delete-unused-secrets", "--project", "p", "--manifest", manifest, "--all"}, wantErr: "cannot be combined"},
		{name: "delete with manifest and service", args: []string{"delete-unused-secrets", "--project", "p", "--manifest", manifest, "--service", "s"}, wantErr: "cannot be combined"},
		{name: "delete all without service", args: []string{"delete-unused-secrets", "--project", "p", "--all"}, wantErr: "--all requires --service"},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			code, stdout, stderr := runArgs(t, tc.args...)
			assert.Equal(t, 2, code)
			assert.Empty(t, stdout)
			assert.Contains(t, stderr, tc.wantErr)
		})
	}
}

func TestErrors(t *testing.T) {
	testCases := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{
			name:    "missing file",
			args:    []string{"generate", "--file", filepath.Join(t.TempDir(), "missing.yaml")},
			wantErr: "no such file or directory",
		},
		{
			name:    "invalid workload",
			args:    []string{"generate", "--file", writeFile(t, "score.json", "metadata: {name: x}")},
			wantErr: "score.json",
		},
		{
			name:    "workload the generator rejects",
			args:    []string{"generate", "--file", writeFile(t, "score.yaml", "apiVersion: score.dev/v1b1\nmetadata: {name: x}\ncontainers: {}\n")},
			wantErr: "a Cloud Run service needs at least one container",
		},
		{
			name:    "manifest without a name",
			args:    []string{"delete-unused-secrets", "--project", "p", "--manifest", writeFile(t, "service.yaml", "kind: Service\n")},
			wantErr: "the manifest has no metadata.name",
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			code, stdout, stderr := runArgs(t, tc.args...)
			assert.Equal(t, 1, code)
			assert.Empty(t, stdout)
			assert.Contains(t, stderr, tc.wantErr)
		})
	}
}

func TestHelp(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"help"}, {"generate", "--help"}, {"delete-unused-secrets", "-h"}} {
		code, _, _ := runArgs(t, args...)
		assert.Equal(t, 0, code, args)
	}
}
