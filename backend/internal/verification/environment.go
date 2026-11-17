package verification

import (
	"net/url"
	"runtime"
	"sort"
	"strconv"
	"strings"
)

// stepEnvironment is one immutable effective child environment. The same
// snapshot feeds every step and the output redactor for the whole run.
type stepEnvironment struct {
	list   []string
	values map[string]string
}

func newStepEnvironment(base []string, overrides map[string]string) stepEnvironment {
	entries := make([]string, 0, len(base)+len(overrides))
	positions := make(map[string]int, len(base)+len(overrides))
	values := make(map[string]string, len(base)+len(overrides))
	add := func(entry string) {
		name, value, ok := splitEnvironmentEntry(entry)
		if !ok {
			entries = append(entries, entry)
			return
		}
		key := environmentKey(name)
		if index, exists := positions[key]; exists {
			entries[index] = entry
		} else {
			positions[key] = len(entries)
			entries = append(entries, entry)
		}
		values[key] = value
	}
	for _, entry := range base {
		add(entry)
	}
	keys := make([]string, 0, len(overrides))
	for key := range overrides {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		add(key + "=" + overrides[key])
	}
	return stepEnvironment{list: entries, values: values}
}

func (e stepEnvironment) Value(name string) string {
	return e.values[environmentKey(name)]
}

func splitEnvironmentEntry(entry string) (string, string, bool) {
	separator := strings.IndexByte(entry, '=')
	if separator == 0 {
		// Windows has hidden per-drive entries such as =C:=C:\\path.
		next := strings.IndexByte(entry[1:], '=')
		if next < 0 {
			return "", "", false
		}
		separator = next + 1
	}
	if separator < 0 {
		return "", "", false
	}
	return entry[:separator], entry[separator+1:], true
}

func environmentKey(name string) string {
	if runtime.GOOS == "windows" {
		return strings.ToUpper(name)
	}
	return name
}

func verificationRedactions(configured map[string]string, env stepEnvironment) map[string]string {
	redactions := make(map[string]string, len(configured))
	for key, value := range configured {
		redactions[key] = value
	}
	index := 0
	for name, value := range env.values {
		if value == "" || (!credentialEnvironmentName(name) && !credentialBearingURL(value)) {
			continue
		}
		redactions["inherited:"+name+":"+strconv.Itoa(index)] = value
		index++
	}
	return redactions
}

func credentialEnvironmentName(name string) bool {
	upper := strings.ToUpper(name)
	for _, marker := range []string{
		"TOKEN", "SECRET", "PASSWORD", "PASSWD", "API_KEY", "APIKEY",
		"PRIVATE_KEY", "ACCESS_KEY", "CREDENTIAL", "AUTHORIZATION",
	} {
		if strings.Contains(upper, marker) {
			return true
		}
	}
	return false
}

func credentialBearingURL(value string) bool {
	parsed, err := url.Parse(value)
	return err == nil && parsed.IsAbs() && parsed.User != nil && parsed.User.Username() != ""
}
