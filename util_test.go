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
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestToSlug(t *testing.T) {
	t.Run("is slug no change", func(t *testing.T) {
		assert.Equal(t, "this-is-a-valid-slug", toSlug("this-is-a-valid-slug"))
	})

	t.Run("ileading dash", func(t *testing.T) {
		assert.Equal(t, "this-is-a-valid-slug", toSlug("-this-is-a-valid-slug"))
	})

	t.Run("has uppercase", func(t *testing.T) {
		assert.Equal(t, "this-is-a-valid-slug", toSlug("This-Is-A-Valid-slug"))
	})

	t.Run("non alphanumeric", func(t *testing.T) {
		assert.Equal(t, "should-t-this-be-invalid", toSlug("Should't this be invalid?"))
	})

	t.Run("runs of dashes", func(t *testing.T) {
		assert.Equal(t, "there-no", toSlug("There---no----"))
	})

	t.Run("runs of dashes from invalid chars", func(t *testing.T) {
		assert.Equal(t, "there-no", toSlug("There... ^&*(\"^)??no!!!"))
	})

	t.Run("rcannot make a valid slug", func(t *testing.T) {
		assert.Equal(t, "", toSlug(""))
	})
}
