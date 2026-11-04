package main

import (
	"encoding/json"
	"os"
)

// loadConfig 从 JSON 文件加载配置。
func loadConfig(path string, cfg *config) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, cfg)
}
