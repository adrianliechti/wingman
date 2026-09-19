package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/adrianliechti/wingman/test/harness"
)

type cliEvent struct {
	Type    string `json:"type"`
	Message string `json:"message"`
	Error   struct {
		Message string `json:"message"`
	} `json:"error"`
	Item struct {
		Type             string `json:"type"`
		Status           string `json:"status"`
		Text             string `json:"text"`
		Command          string `json:"command"`
		AggregatedOutput string `json:"aggregated_output"`
		ExitCode         *int   `json:"exit_code"`
		Changes          []struct {
			Path string `json:"path"`
		} `json:"changes"`
	} `json:"item"`
}

type outcome struct {
	Answer string
	Read   bool
	Edited bool
}

type codexRunOptions struct {
	setup       func(*testing.T, string)
	timeout     time.Duration
	maxRequests int
	// webSearch sets Codex's web_search mode ("disabled", "cached", "live");
	// anything but disabled adds the hosted web_search tool to requests.
	webSearch string
	// autoCompactTokenLimit makes Codex compact the conversation once its
	// token count passes the limit, exercising the compaction protocol.
	autoCompactTokenLimit int
}

func codexEnv(configDir string) []string {
	var env []string
	// Do not inherit provider credentials, Codex configuration, or telemetry.
	for _, key := range []string{"PATH", "HOME", "USER", "TMPDIR", "LANG", "LC_ALL", "SSL_CERT_FILE", "SSL_CERT_DIR", "NODE_EXTRA_CA_CERTS"} {
		if value, ok := os.LookupEnv(key); ok {
			env = append(env, key+"="+value)
		}
	}
	return append(env, "CODEX_HOME="+configDir, "WINGMAN_CODEX_TEST_KEY=wingman-codex-test")
}

func codexConfig(baseURL, model, catalog string, edit bool) string {
	return codexConfigWithOptions(baseURL, model, catalog, edit, codexRunOptions{})
}

func codexConfigWithOptions(baseURL, model, catalog string, edit bool, options codexRunOptions) string {
	sandbox := "read-only"
	if edit {
		sandbox = "workspace-write"
	}
	webSearch := options.webSearch
	if webSearch == "" {
		webSearch = "disabled"
	}
	extra := ""
	if options.autoCompactTokenLimit > 0 {
		extra = fmt.Sprintf("model_auto_compact_token_limit = %d\n", options.autoCompactTokenLimit)
	}
	return fmt.Sprintf(`model = %q
model_provider = "wingman_e2e"
model_catalog_json = %q
model_reasoning_effort = "low"
model_reasoning_summary = "none"
approval_policy = "never"
sandbox_mode = %q
web_search = %q
%sproject_doc_max_bytes = 0
check_for_update_on_startup = false
cli_auth_credentials_store = "ephemeral"
allow_login_shell = false

[model_providers.wingman_e2e]
name = "Wingman CLI comparison"
base_url = %q
env_key = "WINGMAN_CODEX_TEST_KEY"
wire_api = "responses"
requires_openai_auth = false
supports_websockets = false
request_max_retries = 0
stream_max_retries = 0
stream_idle_timeout_ms = 60000

[features]
shell_tool = %t
unified_exec = true
shell_snapshot = false
multi_agent = false
apps = false
remote_plugin = false
hooks = false
enable_request_compression = false
skill_mcp_dependency_install = false

[shell_environment_policy]
inherit = "core"
ignore_default_excludes = false

[sandbox_workspace_write]
network_access = false
exclude_tmpdir_env_var = true
exclude_slash_tmp = true

[history]
persistence = "none"

[analytics]
enabled = false

[feedback]
enabled = false

[otel]
exporter = "none"
`, model, catalog, sandbox, webSearch, extra, baseURL, edit)
}

// Unknown model names otherwise use Codex's fallback metadata, which omits
// apply_patch. Declare the same Responses tool capabilities for every target
// while preserving the actual model name in requests.
func codexCatalog(t *testing.T, model string) []byte {
	t.Helper()
	data, err := json.MarshalIndent(map[string]any{"models": []any{map[string]any{
		"slug": model, "display_name": model, "description": "CLI compatibility test model",
		"base_instructions":          "You are a coding assistant. Complete the user's task using the provided tools. Use the shell tool to read files and the apply_patch tool to edit files. Invoke apply_patch directly, never through the shell. Respect the sandbox and approval settings. Keep the final answer concise.",
		"default_reasoning_level":    "low",
		"supported_reasoning_levels": []any{map[string]any{"effort": "low", "description": "Low reasoning effort"}},
		"shell_type":                 "unified_exec", "visibility": "list", "supported_in_api": true, "priority": 0,
		"apply_patch_tool_type":     "freeform",
		"default_reasoning_summary": "none", "support_verbosity": false,
		"truncation_policy": map[string]any{"mode": "tokens", "limit": 10000},
		"context_window":    128000, "input_modalities": []string{"text"},
		"experimental_supported_tools": []string{},
	}}}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func runCodex(t *testing.T, binary string, endpoint harness.Endpoint, model, prompt, input string) (outcome, []harness.Exchange, string) {
	t.Helper()
	result, exchanges, fixture, _ := runCodexWithOptions(t, binary, endpoint, model, prompt, input, codexRunOptions{})
	return result, exchanges, fixture
}

func runCodexWithOptions(t *testing.T, binary string, endpoint harness.Endpoint, model, prompt, input string, options codexRunOptions) (outcome, []harness.Exchange, string, []cliEvent) {
	t.Helper()
	dir := t.TempDir()
	if root := os.Getenv("CODEX_ARTIFACTS"); root != "" {
		if err := os.MkdirAll(root, 0700); err != nil {
			t.Fatal(err)
		}
		var err error
		dir, err = os.MkdirTemp(root, strings.ReplaceAll(t.Name(), "/", "-")+"-")
		if err != nil {
			t.Fatal(err)
		}
	}
	dir, err := filepath.Abs(dir)
	if err != nil {
		t.Fatal(err)
	}
	// Codex resolves symlinks in file-change events (notably /tmp on macOS).
	dir, err = filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	fixture, configDir := filepath.Join(dir, "fixture"), filepath.Join(dir, "config")
	for _, path := range []string{fixture, configDir} {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if input != "" {
		writeArtifact(t, filepath.Join(fixture, "input.txt"), []byte(input))
		writeArtifact(t, filepath.Join(fixture, "output.txt"), []byte("REPLACE_ME\n"))
		prompt += fmt.Sprintf("\nThe files are %q and %q. Use the current directory and do not access other paths.", filepath.Join(fixture, "input.txt"), filepath.Join(fixture, "output.txt"))
	}
	if options.setup != nil {
		options.setup(t, fixture)
		prompt += fmt.Sprintf("\nThe project directory is %q, which is already the current working directory. Use that directory for every command and patch. Do not access other paths.", fixture)
	}
	if options.maxRequests == 0 {
		options.maxRequests = 8
	}
	if options.timeout == 0 {
		options.timeout = 2 * time.Minute
	}
	r := harness.NewRecorder(t, strings.TrimRight(endpoint.BaseURL, "/"), http.Header{"Authorization": {"Bearer " + endpoint.APIKey}}, options.maxRequests)
	catalog := filepath.Join(configDir, "models.json")
	writeArtifact(t, catalog, codexCatalog(t, model))
	writeArtifact(t, filepath.Join(configDir, "config.toml"), []byte(codexConfigWithOptions(r.URL, model, catalog, input != "" || options.setup != nil, options)))
	ctx, cancel := context.WithTimeout(t.Context(), options.timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "exec", "--strict-config", "--json", "--ephemeral", "--ignore-rules", "--skip-git-repo-check", "--color", "never", "--", prompt)
	cmd.Dir, cmd.Env, cmd.WaitDelay = fixture, codexEnv(configDir), 5*time.Second
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	runErr := cmd.Run()
	r.Close()
	exchanges := r.Exchanges()
	writeArtifact(t, filepath.Join(dir, "stdout.jsonl"), stdout.Bytes())
	writeArtifact(t, filepath.Join(dir, "stderr.log"), stderr.Bytes())
	data, err := json.MarshalIndent(exchanges, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	writeArtifact(t, filepath.Join(dir, "http.json"), data)
	t.Logf("%s/%s: %d HTTP exchanges; artifacts: %s", endpoint.Name, model, len(exchanges), dir)
	if runErr != nil {
		t.Errorf("Codex failed: %v (context: %v)\n%s", runErr, ctx.Err(), stderr.String())
	}
	decoder := json.NewDecoder(&stdout)
	var result outcome
	var events []cliEvent
	completed := false
	for {
		var event cliEvent
		if err := decoder.Decode(&event); err == io.EOF {
			break
		} else if err != nil {
			t.Fatalf("invalid Codex JSON output: %v", err)
		}
		events = append(events, event)
		switch event.Type {
		case "turn.completed":
			if completed {
				t.Error("multiple completed Codex turns")
			}
			completed = true
		case "turn.failed", "error":
			t.Errorf("Codex reported %s: %s%s", event.Type, event.Message, event.Error.Message)
		case "item.completed":
			switch event.Item.Type {
			case "agent_message":
				result.Answer = strings.TrimSpace(event.Item.Text)
			case "command_execution":
				// A valid read may print text, a quoted string, or a byte dump.
				// The unpredictable final fixture verifies the data was read.
				readInput := input == "" || (strings.Contains(event.Item.Command, "input.txt") && event.Item.AggregatedOutput != "")
				if event.Item.Status == "completed" && event.Item.ExitCode != nil && *event.Item.ExitCode == 0 && readInput {
					result.Read = true
				}
			case "file_change":
				if event.Item.Status == "completed" {
					for _, change := range event.Item.Changes {
						if options.setup != nil || change.Path == filepath.Join(fixture, "output.txt") || change.Path == "output.txt" {
							result.Edited = true
						}
					}
				}
			}
		}
	}
	if !completed || result.Answer == "" {
		t.Fatalf("Codex did not complete with a final answer; stderr: %s", stderr.String())
	}
	return result, exchanges, fixture, events
}

func writeArtifact(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func codexBinary(t *testing.T, required bool) string {
	t.Helper()
	name := os.Getenv("CODEX_BIN")
	if name == "" {
		name = "codex"
	}
	binary, err := exec.LookPath(name)
	if err != nil {
		if required || os.Getenv("CODEX_BIN") != "" {
			t.Fatalf("Codex CLI is required: %v", err)
		}
		t.Skip("Codex CLI is not installed")
	}
	binary, err = filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	return binary
}
