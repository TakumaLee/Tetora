package main

import (
	"encoding/json"
	"testing"
)

func testConfig() *Config {
	return &Config{
		ListenAddr: "127.0.0.1:7777",
	}
}

func TestBuildOpenAPISpec_HasRequiredFields(t *testing.T) {
	spec := buildOpenAPISpec(testConfig())

	requiredKeys := []string{"openapi", "info", "paths", "components", "tags"}
	for _, key := range requiredKeys {
		if _, ok := spec[key]; !ok {
			t.Errorf("spec missing required key %q", key)
		}
	}

	info, ok := spec["info"].(map[string]any)
	if !ok {
		t.Fatal("info is not a map")
	}
	for _, key := range []string{"title", "description", "version"} {
		if _, ok := info[key]; !ok {
			t.Errorf("info missing required key %q", key)
		}
	}
}

func TestBuildOpenAPISpec_Version(t *testing.T) {
	spec := buildOpenAPISpec(testConfig())
	info := spec["info"].(map[string]any)
	version, ok := info["version"].(string)
	if !ok {
		t.Fatal("version is not a string")
	}
	if version != tetoraVersion {
		t.Errorf("expected version %q, got %q", tetoraVersion, version)
	}
}

func TestBuildOpenAPISpec_OpenAPIVersion(t *testing.T) {
	spec := buildOpenAPISpec(testConfig())
	v, ok := spec["openapi"].(string)
	if !ok || v != "3.0.3" {
		t.Errorf("expected openapi 3.0.3, got %v", spec["openapi"])
	}
}

func TestBuildOpenAPISpec_AllEndpoints(t *testing.T) {
	spec := buildOpenAPISpec(testConfig())
	paths, ok := spec["paths"].(map[string]any)
	if !ok {
		t.Fatal("paths is not a map")
	}

	expectedPaths := []string{
		"/dispatch",
		"/dispatch/estimate",
		"/cancel",
		"/healthz",
		"/history",
		"/history/{id}",
		"/sessions",
		"/sessions/{id}",
		"/sessions/{id}/message",
		"/sessions/{id}/stream",
		"/workflows",
		"/workflows/{name}",
		"/workflows/{name}/run",
		"/workflow-runs",
		"/workflow-runs/{id}",
		"/knowledge",
		"/knowledge/search",
		"/circuits",
		"/circuits/{provider}/reset",
		"/queue",
		"/budget",
		"/budget/pause",
		"/budget/resume",
		"/cron",
		"/cron/{id}/trigger",
		"/agent-messages",
		"/handoffs",
		"/roles",
		"/roles/{name}",
		"/route",
		"/audit",
		"/backup",
		"/stats/cost",
		"/stats/sla",
	}

	for _, path := range expectedPaths {
		if _, ok := paths[path]; !ok {
			t.Errorf("missing path: %s", path)
		}
	}
}

func TestBuildOpenAPISpec_Schemas(t *testing.T) {
	spec := buildOpenAPISpec(testConfig())
	components, ok := spec["components"].(map[string]any)
	if !ok {
		t.Fatal("components is not a map")
	}
	schemas, ok := components["schemas"].(map[string]any)
	if !ok {
		t.Fatal("schemas is not a map")
	}

	expectedSchemas := []string{
		"Task",
		"TaskResult",
		"DispatchResult",
		"Session",
		"SessionDetail",
		"SessionMessage",
		"Workflow",
		"WorkflowStep",
		"WorkflowRun",
		"StepRunResult",
		"CostEstimate",
		"EstimateResult",
		"KnowledgeFile",
		"SearchResult",
		"Handoff",
		"AgentMessage",
		"QueueItem",
		"SLAMetrics",
		"HealthResult",
		"CronJob",
		"AgentConfig",
		"SmartDispatchResult",
		"Error",
		"JobRun",
		"AuditEntry",
	}

	for _, name := range expectedSchemas {
		if _, ok := schemas[name]; !ok {
			t.Errorf("missing schema: %s", name)
		}
	}
}

func TestBuildOpenAPISpec_ValidJSON(t *testing.T) {
	spec := buildOpenAPISpec(testConfig())
	data, err := json.Marshal(spec)
	if err != nil {
		t.Fatalf("failed to marshal spec to JSON: %v", err)
	}
	if len(data) == 0 {
		t.Error("marshaled spec is empty")
	}

	// Verify it round-trips.
	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("failed to unmarshal spec JSON: %v", err)
	}
	if parsed["openapi"] != "3.0.3" {
		t.Error("round-tripped spec missing openapi field")
	}
}

func TestBuildOpenAPISpec_SecurityScheme(t *testing.T) {
	// With token: security should be present.
	cfgWithToken := &Config{
		ListenAddr: "127.0.0.1:7777",
		APIToken:   "test-secret-token",
	}
	spec := buildOpenAPISpec(cfgWithToken)
	if _, ok := spec["security"]; !ok {
		t.Error("security should be present when APIToken is set")
	}

	// Without token: security should be absent.
	cfgNoToken := &Config{
		ListenAddr: "127.0.0.1:7777",
	}
	specNoToken := buildOpenAPISpec(cfgNoToken)
	if _, ok := specNoToken["security"]; ok {
		t.Error("security should not be present when APIToken is empty")
	}

	// Security scheme should always be in components.
	components := spec["components"].(map[string]any)
	secSchemes, ok := components["securitySchemes"].(map[string]any)
	if !ok {
		t.Fatal("securitySchemes not found in components")
	}
	if _, ok := secSchemes["bearerAuth"]; !ok {
		t.Error("bearerAuth security scheme not found")
	}
}

func TestBuildOpenAPISpec_ServerURL(t *testing.T) {
	cfg := &Config{ListenAddr: "0.0.0.0:9999"}
	spec := buildOpenAPISpec(cfg)
	servers, ok := spec["servers"].([]map[string]any)
	if !ok || len(servers) == 0 {
		t.Fatal("servers is missing or empty")
	}
	url, ok := servers[0]["url"].(string)
	if !ok || url != "http://0.0.0.0:9999" {
		t.Errorf("expected server url http://0.0.0.0:9999, got %v", url)
	}
}

func TestBuildOpenAPISpec_DispatchEndpointShape(t *testing.T) {
	spec := buildOpenAPISpec(testConfig())
	paths := spec["paths"].(map[string]any)
	dispatch := paths["/dispatch"].(map[string]any)
	post, ok := dispatch["post"].(map[string]any)
	if !ok {
		t.Fatal("/dispatch POST operation not found")
	}

	// Should have tags, summary, description, requestBody, responses.
	for _, key := range []string{"tags", "summary", "description", "requestBody", "responses"} {
		if _, ok := post[key]; !ok {
			t.Errorf("/dispatch POST missing %q", key)
		}
	}

	// Tags should include "Core".
	tags, ok := post["tags"].([]string)
	if !ok || len(tags) == 0 || tags[0] != "Core" {
		t.Errorf("/dispatch POST should be tagged Core, got %v", post["tags"])
	}
}

func TestBuildPaths_MethodTypes(t *testing.T) {
	paths := buildPaths()

	// /dispatch should only have POST.
	dispatch := paths["/dispatch"].(map[string]any)
	if _, ok := dispatch["post"]; !ok {
		t.Error("/dispatch missing post")
	}
	if _, ok := dispatch["get"]; ok {
		t.Error("/dispatch should not have get")
	}

	// /healthz should only have GET.
	healthz := paths["/healthz"].(map[string]any)
	if _, ok := healthz["get"]; !ok {
		t.Error("/healthz missing get")
	}

	// /sessions/{id} should have GET and DELETE.
	sessId := paths["/sessions/{id}"].(map[string]any)
	if _, ok := sessId["get"]; !ok {
		t.Error("/sessions/{id} missing get")
	}
	if _, ok := sessId["delete"]; !ok {
		t.Error("/sessions/{id} missing delete")
	}
}

func TestBuildComponents_ErrorSchema(t *testing.T) {
	components := buildComponents()
	schemas := components["schemas"].(map[string]any)
	errSchema := schemas["Error"].(map[string]any)

	props, ok := errSchema["properties"].(map[string]any)
	if !ok {
		t.Fatal("Error schema missing properties")
	}
	if _, ok := props["error"]; !ok {
		t.Error("Error schema missing 'error' property")
	}

	required, ok := errSchema["required"].([]string)
	if !ok || len(required) == 0 || required[0] != "error" {
		t.Error("Error schema 'error' field should be required")
	}
}
