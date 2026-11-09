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
	"encoding/json"
	"testing"

	"github.com/score-spec/score-go/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	servingv1 "knative.dev/serving/pkg/apis/serving/v1"

	"github.com/humanitec/cloudrun-score-generator/score"
)

// A generator is not bound to one service: each workload names its own service and secrets.
func TestGenerateMultipleWorkloads(t *testing.T) {
	substitutions := map[string]score.SubValue{
		"resources.db.password": {Secret: &score.SecretRef{Type: score.DirectSecretType, Value: "s3cr3t"}},
	}
	workloadFor := func(name string) types.Workload {
		return types.Workload{
			Metadata: types.WorkloadMetadata{"name": name},
			Containers: types.WorkloadContainers{"main": {
				Image:     "busybox:latest",
				Variables: types.ContainerVariables{"WORKLOAD": name, "PASSWORD": "${resources.db.password}"},
			}},
			Resources: types.WorkloadResources{"db": {Type: "postgres"}},
		}
	}
	saver := newFakeSecretSaver()
	gen := NewGenerator(saver)

	for _, tc := range []struct{ workload, service string }{
		{workload: "one", service: "one-dev"},
		{workload: "two", service: "two-dev"},
	} {
		workload := WithServiceName(workloadFor(tc.workload), tc.service)
		manifests, err := gen.Generate(t.Context(), &workload, substitutions)
		require.NoError(t, err)

		var service servingv1.Service
		require.NoError(t, json.Unmarshal(manifests["service"], &service))
		assert.Equal(t, tc.service, service.Name)
		env := service.Spec.Template.Spec.Containers[0].Env
		require.Len(t, env, 2)
		assert.Equal(t, tc.workload, env[1].Value, "each workload keeps its own variables")

		secretName := secretID(tc.service, "main", "env", "PASSWORD")
		assert.Equal(t, secretName, env[0].ValueFrom.SecretKeyRef.Name)
		assert.Equal(t, tc.service, saver.services[secretName], "the secret is saved for its own service")
	}
}

// A service block without containers has no container to put the port on.
func TestGenerateNoContainers(t *testing.T) {
	workload := types.Workload{
		Metadata: types.WorkloadMetadata{"name": "hello-world"},
		Service:  &types.WorkloadService{},
	}

	manifests, err := NewGenerator(newFakeSecretSaver()).Generate(t.Context(), &workload, nil)

	require.ErrorContains(t, err, "needs at least one container")
	assert.Nil(t, manifests)
}

// Without a name, secrets would be saved under "" before Cloud Run rejects the service.
func TestGenerateNoServiceName(t *testing.T) {
	workload := types.Workload{
		Containers: types.WorkloadContainers{"main": {
			Image:     "busybox:latest",
			Variables: types.ContainerVariables{"PASSWORD": "${resources.db.password}"},
		}},
		Resources: types.WorkloadResources{"db": {Type: "postgres"}},
	}
	substitutions := map[string]score.SubValue{
		"resources.db.password": {Secret: &score.SecretRef{Type: score.DirectSecretType, Value: "s3cr3t"}},
	}
	saver := newFakeSecretSaver()

	manifests, err := NewGenerator(saver).Generate(t.Context(), &workload, substitutions)

	require.ErrorContains(t, err, "needs a name")
	assert.Nil(t, manifests)
	assert.Empty(t, saver.services, "no secret is saved")
}

func TestGenerateNilWorkload(t *testing.T) {
	manifests, err := NewGenerator(newFakeSecretSaver()).Generate(t.Context(), nil, nil)

	require.ErrorContains(t, err, "no workload")
	assert.Nil(t, manifests)
}
