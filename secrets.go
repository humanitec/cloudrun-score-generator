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

package cloudrun

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"slices"
	"strings"

	servingv1 "knative.dev/serving/pkg/apis/serving/v1"
	"sigs.k8s.io/yaml"
)

// illegalChars matches characters not allowed in a Google Secret Manager
// secret ID.
var illegalChars = regexp.MustCompile(`[^A-Za-z0-9_-]`)

const (
	// maxNameLen is the longest secret ID Google Secret Manager accepts.
	maxNameLen = 255
)

// secretID builds a Google Secret Manager secret ID from a workload name, container name, kind and name
// of the variable. Secret Manager only accepts [A-Za-z0-9_-]{1,255}.
func secretID(workloadName, containerName, kind, key string) string {
	parts := []string{workloadName, containerName, kind, key}
	h := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	suffix := hex.EncodeToString(h[:])

	name := illegalChars.ReplaceAllString(strings.Join(parts, "_"), "_")
	if maxLen := maxNameLen - 64 - 1; len(name) > maxLen {
		name = name[:maxLen]
	}
	return name + "_" + suffix
}

// ReferencedSecrets returns the sorted, unique names of the Secret Manager secrets that a
// Cloud Run service manifest references in environment variables and secret volumes.
func ReferencedSecrets(manifest []byte) ([]string, error) {
	var service servingv1.Service
	if err := yaml.Unmarshal(manifest, &service); err != nil {
		return nil, fmt.Errorf("parsing the service manifest: %w", err)
	}

	spec := service.Spec.Template.Spec
	var names []string
	for _, container := range spec.Containers {
		for _, env := range container.Env {
			if env.ValueFrom != nil && env.ValueFrom.SecretKeyRef != nil {
				names = append(names, env.ValueFrom.SecretKeyRef.Name)
			}
		}
	}
	for _, volume := range spec.Volumes {
		if volume.Secret != nil {
			names = append(names, volume.Secret.SecretName)
		}
	}
	slices.Sort(names)
	return slices.Compact(names), nil
}
