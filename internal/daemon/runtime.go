package daemon

import (
	"encoding/json"
	"os"
)

type Runtime struct {
	Address string `json:"address"`
	NodeID  string `json:"node_id"`
	Version string `json:"version"`
}

func ReadRuntime(path string) (Runtime, error) {
	var state Runtime
	data, err := os.ReadFile(path)
	if err != nil {
		return state, err
	}
	err = json.Unmarshal(data, &state)
	return state, err
}

func writeRuntime(file *os.File, state Runtime) error {
	if err := file.Truncate(0); err != nil {
		return err
	}
	if _, err := file.Seek(0, 0); err != nil {
		return err
	}
	return json.NewEncoder(file).Encode(state)
}
