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

// Package score defines the contract between Score provisioners and generators.
//
// It is a temporary copy of the types proposed for github.com/score-spec/score-go/v2/score
// (https://github.com/score-spec/score-go/pull/239) and will be replaced by that package
// once it is released.
package score

import (
	"context"

	"github.com/score-spec/score-go/types"
)

type Generator interface {
	// Generate produces deployable artefacts for the target platform.
	Generate(context.Context, *types.Workload, map[string]SubValue) (map[string][]byte, error)
}
