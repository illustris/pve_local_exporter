package pveconfig

import (
	"regexp"
	"strings"
)

// StorageEntry holds a parsed storage definition from storage.cfg.
type StorageEntry struct {
	Properties map[string]string
}

var sanitizeRe = regexp.MustCompile(`[^a-zA-Z0-9_]`)

// SanitizeKey replaces non-alphanumeric/underscore chars with underscore.
func SanitizeKey(key string) string {
	return sanitizeRe.ReplaceAllString(key, "_")
}

// ParseStorageConfig parses /etc/pve/storage.cfg content.
// Returns a list of storage entries, each with sanitized key-value properties.
func ParseStorageConfig(data string) []StorageEntry {
	var result []StorageEntry
	var current *StorageEntry

	for _, line := range strings.Split(data, "\n") {
		line = strings.TrimSpace(line)

		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		if strings.Contains(line, ":") && !strings.HasPrefix(line, "\t") && !strings.HasPrefix(line, " ") {
			// Check if this is a section header (type: name)
			colonIdx := strings.Index(line, ":")
			sectionType := strings.TrimSpace(line[:colonIdx])
			sectionName := strings.TrimSpace(line[colonIdx+1:])

			// Only treat as header if type has no spaces (it's a single word)
			if !strings.Contains(sectionType, " ") {
				if current != nil {
					result = append(result, *current)
				}
				current = &StorageEntry{
					Properties: map[string]string{
						"type": SanitizeKey(sectionType),
						"name": SanitizeKey(sectionName),
					},
				}
				continue
			}
		}

		// Key-value property line
		if current != nil {
			parts := strings.SplitN(line, " ", 2)
			key := SanitizeKey(strings.TrimSpace(parts[0]))
			if len(parts) > 1 {
				current.Properties[key] = strings.TrimSpace(parts[1])
			} else {
				current.Properties[key] = "true"
			}
		}
	}

	if current != nil {
		result = append(result, *current)
	}

	return result
}
