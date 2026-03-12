package pveconfig

import (
	"regexp"
	"strings"

	"pve_local_exporter/internal/logging"
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

	for _, rawLine := range strings.Split(data, "\n") {
		trimmed := strings.TrimSpace(rawLine)

		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		// Section headers start at column 0 (no leading whitespace)
		isIndented := len(rawLine) > 0 && (rawLine[0] == '\t' || rawLine[0] == ' ')

		if !isIndented && strings.Contains(trimmed, ":") {
			colonIdx := strings.Index(trimmed, ":")
			sectionType := trimmed[:colonIdx]
			sectionName := strings.TrimSpace(trimmed[colonIdx+1:])

			if current != nil {
				result = append(result, *current)
			}
			current = &StorageEntry{
				Properties: map[string]string{
					"type": SanitizeKey(sectionType),
					"name": SanitizeKey(sectionName),
				},
			}
			logging.Trace("storage.cfg section", "type", sectionType, "name", sectionName)
			continue
		}

		// Key-value property line
		if current != nil {
			parts := strings.SplitN(trimmed, " ", 2)
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

	logging.Trace("ParseStorageConfig complete", "entries", len(result))
	return result
}
