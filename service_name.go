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
	"cmp"
	"maps"

	"github.com/score-spec/score-go/v2/score"
)

// ServiceName returns the Cloud Run service name for the workload:
// the ServiceNameAnnotation if set, metadata.name otherwise.
func ServiceName(spec *score.Workload) string {
	annotations, _ := spec.Metadata["annotations"].(map[string]any)
	name, _ := annotations[ServiceNameAnnotation].(string)
	workloadName, _ := spec.Metadata["name"].(string)
	return cmp.Or(name, workloadName)
}

// WithServiceName returns a copy of the workload with the ServiceNameAnnotation set to name.
func WithServiceName(spec score.Workload, name string) score.Workload {
	metadata := maps.Clone(spec.Metadata)
	if metadata == nil {
		metadata = score.WorkloadMetadata{}
	}
	annotations, _ := metadata["annotations"].(map[string]any)
	annotations = maps.Clone(annotations)
	if annotations == nil {
		annotations = map[string]any{}
	}
	annotations[ServiceNameAnnotation] = name
	metadata["annotations"] = annotations
	spec.Metadata = metadata
	return spec
}
