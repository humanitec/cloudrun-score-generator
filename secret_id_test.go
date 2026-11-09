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
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// validSecretID is what Google Secret Manager accepts as a secret ID.
var validSecretID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,255}$`)

func TestNameIsAlwaysAValidSecretID(t *testing.T) {
	keys := []string{
		"config.yaml",
		".env",
		"DB_URL",
		"a file with spaces",
		"naïve.json",
		"nested.dotted.conf",
		strings.Repeat("very-long-file-name.", 40),
	}

	for _, key := range keys {
		t.Run(key, func(t *testing.T) {
			name := secretID("hello-world-dev", "main", "vol", key)
			assert.Regexp(t, validSecretID, name)
			assert.LessOrEqual(t, len(name), maxNameLen)
		})
	}
}

// The name has to survive redeployment unchanged, otherwise every deployment
// creates a fresh secret instead of adding a version to the existing one.
func TestNameIsDeterministic(t *testing.T) {
	assert.Equal(t,
		secretID("hello-world-dev", "main", "env", "DB_URL"),
		secretID("hello-world-dev", "main", "env", "DB_URL"),
	)
}

func TestNameDistinguishesAmbiguousParts(t *testing.T) {
	testCases := []struct {
		name string
		a, b [4]string
	}{
		{
			// Both render the same readable prefix, so only the hash separates them.
			name: "the separator also occurs inside a part",
			a:    [4]string{"app", "b_vol_c", "vol", "d"},
			b:    [4]string{"app", "b", "vol", "c_vol_d"},
		},
		{
			// Sanitising turns both dots and dashes into "_".
			name: "sanitising collapses two distinct file names",
			a:    [4]string{"app", "main", "vol", "config.yaml"},
			b:    [4]string{"app", "main", "vol", "config-yaml"},
		},
		{
			name: "an env var and a file share a key",
			a:    [4]string{"app", "main", "env", "TOKEN"},
			b:    [4]string{"app", "main", "vol", "TOKEN"},
		},
		{
			name: "different containers",
			a:    [4]string{"app", "main", "env", "TOKEN"},
			b:    [4]string{"app", "sidecar", "env", "TOKEN"},
		},
		{
			// Secrets are project-global, so two environments in one project must not collide.
			name: "different workloads",
			a:    [4]string{"app-dev", "main", "env", "TOKEN"},
			b:    [4]string{"app-prod", "main", "env", "TOKEN"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			assert.NotEqual(t,
				secretID(tc.a[0], tc.a[1], tc.a[2], tc.a[3]),
				secretID(tc.b[0], tc.b[1], tc.b[2], tc.b[3]),
			)
		})
	}
}
