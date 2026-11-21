package fork

import "sigs.k8s.io/yaml"

func yamlToJSON(s string) ([]byte, error) {
	return yaml.YAMLToJSON([]byte(s))
}
