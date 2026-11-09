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
	"fmt"
	"regexp"
	"strings"
)

func decodeViaJSON(in, out any) error {
	objAsBytes, err := json.Marshal(in)
	if err != nil {
		return fmt.Errorf("unable to serialize input to json: %w", err)
	}
	err = json.Unmarshal(objAsBytes, out)
	if err != nil {
		return fmt.Errorf("unable to deserialize output from json: %w", err)
	}
	return err
}

func decodeToString(in any) (string, error) {
	if str, ok := in.(string); ok {
		return str, nil
	}
	b, err := json.Marshal(in)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

const slugRegexPattern = "[a-z0-9](?:-?[a-z0-9]+)*"

var slugRegex = regexp.MustCompile("^" + slugRegexPattern + "$")

var illegalSlugChars = regexp.MustCompile(`[^0-9a-z-]`)
var runsOfDashes = regexp.MustCompile(`--+`)

// isValidSlug checks whether the supplied string is a slug.
//
// A slug:
//
// - only has alphanumeric lowercase values an "-"
//
// - does not start or end with a "-"
//
// - does not have two or more "-" in sequence.
func isValidSlug(slug string) bool {
	return slugRegex.MatchString(slug)
}

// toSlug converts any string into a slug.
//
// Process:
//
// - all uppercase -> lowercase
//
// - all non alphanumeric converted to "-"
//
// - runs of "-"" replaced with a single dash
//
// - leading and trailing dashes removed
//
// NOTE: if a stringc cannot be turned into a slug, an empty string is
// returned.
func toSlug(s string) string {
	return strings.Trim(runsOfDashes.ReplaceAllString(illegalSlugChars.ReplaceAllString(strings.ToLower(s), "-"), "-"), "-")
}
