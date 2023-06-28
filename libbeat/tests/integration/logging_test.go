//go:build integration

package integration

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoggingConsoleECS(t *testing.T) {
	mockbeat := NewBeat(t, "mockbeat", "../../libbeat.test", "-E", "http.enabled=true", "-e")
	mockbeat.WriteConfigFile(cfg)
	mockbeat.Start()
	line := mockbeat.WaitStdErrContains("ecs.version", 60*time.Second)

	var m map[string]any
	require.NoError(t, json.Unmarshal([]byte(line), &m), "Unmarshaling log line as json")

	_, ok := m["log.level"]
	assert.True(t, ok)

	_, ok = m["@timestamp"]
	assert.True(t, ok)

	_, ok = m["message"]
	assert.True(t, ok)
}

func TestLoggingFileDefault(t *testing.T) {
	startMockBeat(t, "Mockbeat is alive!")
}
