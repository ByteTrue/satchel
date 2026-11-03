package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMagpieToolSchemas(t *testing.T) {
	server := New(Options{Modules: ModuleState{Vision: true, Image: true, WebSearch: true}})
	session := connectTestClient(t, server)
	list, err := session.ListTools(context.Background(), &mcp.ListToolsParams{})
	if err != nil {
		t.Fatal(err)
	}
	tools := make(map[string]*mcp.Tool)
	for _, tool := range list.Tools {
		tools[tool.Name] = tool
	}
	for _, name := range []string{"analyze_image", "generate_image", "generate_video", "generation_result", "web_search", "web_fetch"} {
		if tools[name] == nil {
			t.Errorf("missing Magpie tool %q", name)
		}
	}
	if tools["image_ask"] != nil {
		t.Error("legacy image_ask is exposed")
	}
	assertSchema := func(name string, required string, check func(map[string]any)) {
		t.Helper()
		data, err := json.Marshal(tools[name].InputSchema)
		if err != nil {
			t.Fatal(err)
		}
		var schema map[string]any
		if err := json.Unmarshal(data, &schema); err != nil {
			t.Fatal(err)
		}
		requiredJSON, _ := json.Marshal(schema["required"])
		if !strings.Contains(string(requiredJSON), strconv.Quote(required)) {
			t.Errorf("%s required = %s", name, requiredJSON)
		}
		check(schema)
	}
	assertSchema("analyze_image", "image", func(map[string]any) {})
	assertSchema("generate_image", "prompt", func(schema map[string]any) {
		n := schema["properties"].(map[string]any)["n"].(map[string]any)
		if n["minimum"] != float64(1) || n["maximum"] != float64(4) {
			t.Errorf("generate_image n schema = %+v", n)
		}
	})
	assertSchema("generate_video", "prompt", func(schema map[string]any) {
		seconds := schema["properties"].(map[string]any)["seconds"].(map[string]any)
		if seconds["minimum"] != float64(1) || seconds["maximum"] != float64(15) {
			t.Errorf("generate_video seconds schema = %+v", seconds)
		}
	})
	assertSchema("generation_result", "id", func(map[string]any) {})
	assertSchema("web_search", "query", func(schema map[string]any) {
		if _, found := schema["properties"].(map[string]any)["max_results"]; found {
			t.Error("web_search exposes non-Magpie max_results")
		}
	})
}

func TestGenerateImageEndToEndOverMCP(t *testing.T) {
	server := New(Options{Modules: ModuleState{Image: true}, Generate: func(_ context.Context, input GenerateInput) (GenerationResult, error) {
		if input.Prompt != "a cat" || input.N != 1 {
			return GenerationResult{}, errors.New("unexpected input")
		}
		return GenerationResult{Text: "provider finished", Files: []string{"/tmp/cat.png"}}, nil
	}, GenerationWait: time.Second})
	session := connectTestClient(t, server)
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "generate_image", Arguments: map[string]any{"prompt": "a cat"},
	})
	if err != nil || result.IsError {
		t.Fatalf("CallTool = %+v, %v", result, err)
	}
	text := resultText(t, result)
	for _, want := range []string{"provider finished", "/tmp/cat.png"} {
		if !strings.Contains(text, want) {
			t.Errorf("result %q does not contain %q", text, want)
		}
	}
}

func TestGenerationTaskRoundTripOverMCP(t *testing.T) {
	release := make(chan struct{})
	server := New(Options{Modules: ModuleState{Image: true}, Generate: func(ctx context.Context, _ GenerateInput) (GenerationResult, error) {
		select {
		case <-release:
			return GenerationResult{Files: []string{"/tmp/slow.png"}}, nil
		case <-ctx.Done():
			return GenerationResult{}, ctx.Err()
		}
	}, GenerationWait: 20 * time.Millisecond})
	session := connectTestClient(t, server)
	first, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "generate_image", Arguments: map[string]any{"prompt": "slow"},
	})
	if err != nil || first.IsError {
		t.Fatalf("first CallTool = %+v, %v", first, err)
	}
	structured, ok := first.StructuredContent.(map[string]any)
	if !ok || structured["status"] != "pending" || structured["id"] == "" {
		t.Fatalf("pending structured result = %#v", first.StructuredContent)
	}
	close(release)
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "generation_result", Arguments: map[string]any{"id": structured["id"]},
	})
	if err != nil || result.IsError || !strings.Contains(resultText(t, result), "/tmp/slow.png") {
		t.Fatalf("generation_result = %+v, %v", result, err)
	}
}

func TestDisabledGenerationIsNotListedOrCallable(t *testing.T) {
	called := false
	server := New(Options{Generate: func(context.Context, GenerateInput) (GenerationResult, error) {
		called = true
		return GenerationResult{}, nil
	}, GenerationWait: time.Millisecond})
	session := connectTestClient(t, server)
	list, listErr := session.ListTools(context.Background(), &mcp.ListToolsParams{})
	if listErr == nil && len(list.Tools) != 0 {
		t.Fatalf("only management tools should be listed: %+v", list.Tools)
	}
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "generate_image", Arguments: map[string]any{"prompt": "should not run"},
	})
	if err == nil && (result == nil || !result.IsError) {
		t.Fatalf("disabled tool call succeeded: %+v", result)
	}
	if called {
		t.Fatal("disabled GenerateFunc was called")
	}
}

func TestGenerateImageValidation(t *testing.T) {
	server := New(Options{Modules: ModuleState{Image: true}, Generate: func(context.Context, GenerateInput) (GenerationResult, error) {
		t.Fatal("generator called for invalid input")
		return GenerationResult{}, nil
	}, GenerationWait: time.Millisecond})
	session := connectTestClient(t, server)
	for _, args := range []map[string]any{{"prompt": " "}, {"prompt": "x", "n": 5}} {
		result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "generate_image", Arguments: args})
		if err != nil {
			t.Fatal(err)
		}
		if !result.IsError {
			t.Fatalf("invalid input succeeded: %+v", result)
		}
	}
}

func TestAnalyzeImageToolAndSwitch(t *testing.T) {
	server := New(Options{Modules: ModuleState{Vision: true}, AnalyzeImage: func(_ context.Context, input AnalyzeImageInput) (AnalyzeImageResult, error) {
		if input.Image != "/tmp/image.png" || input.Prompt != "what is here" {
			t.Errorf("analyze_image input = %+v", input)
		}
		return AnalyzeImageResult{Text: "a test image"}, nil
	}})
	session := connectTestClient(t, server)
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "analyze_image", Arguments: map[string]any{"image": "/tmp/image.png", "prompt": "what is here"}})
	if err != nil || result.IsError || resultText(t, result) != "a test image" {
		t.Fatalf("analyze_image = %+v, %v", result, err)
	}

	disabled := New(Options{Modules: ModuleState{Image: true}})
	session = connectTestClient(t, disabled)
	list, err := session.ListTools(context.Background(), &mcp.ListToolsParams{})
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range list.Tools {
		if tool.Name == "analyze_image" || tool.Name == "image_ask" {
			t.Fatalf("unexpected vision tool %q", tool.Name)
		}
	}
	result, err = session.CallTool(context.Background(), &mcp.CallToolParams{Name: "analyze_image", Arguments: map[string]any{"image": "/tmp/image.png"}})
	if err == nil && !result.IsError {
		t.Fatal("disabled analyze_image executed")
	}
}

func TestWebModuleControlsBothTools(t *testing.T) {
	server := New(Options{
		Modules: ModuleState{WebSearch: true},
		WebSearch: func(context.Context, WebSearchInput) (WebSearchResult, error) {
			return WebSearchResult{Text: "search ok", Provider: "fake"}, nil
		},
		WebFetch: func(context.Context, WebFetchInput) (WebFetchResult, error) {
			return WebFetchResult{Text: "fetch ok", URL: "https://example.test"}, nil
		},
	})
	session := connectTestClient(t, server)
	search, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "web_search", Arguments: map[string]any{"query": "test"}})
	if err != nil || search.IsError || resultText(t, search) != "search ok" {
		t.Fatalf("web_search = %+v, %v", search, err)
	}
	fetch, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "web_fetch", Arguments: map[string]any{"url": "https://example.test"}})
	if err != nil || fetch.IsError || resultText(t, fetch) != "fetch ok" {
		t.Fatalf("web_fetch = %+v, %v", fetch, err)
	}
}

func TestModuleToolMatrix(t *testing.T) {
	for mask := 0; mask < 8; mask++ {
		state := ModuleState{Vision: mask&1 != 0, Image: mask&2 != 0, WebSearch: mask&4 != 0}
		session := connectTestClient(t, New(Options{Modules: state}))
		list, err := session.ListTools(context.Background(), &mcp.ListToolsParams{})
		if err != nil {
			t.Fatal(err)
		}
		want := 0
		for _, module := range []Module{ModuleVision, ModuleImage, ModuleWebSearch} {
			if state.Enabled(module) {
				want += len(moduleTools[module])
			}
		}
		if len(list.Tools) != want {
			t.Errorf("state %+v has %d tools, want %d", state, len(list.Tools), want)
		}
	}
}

func TestAgentHasNoModuleManagementTools(t *testing.T) {
	server := New(Options{Modules: ModuleState{Vision: true, Image: true, WebSearch: true}})
	session := connectTestClient(t, server)
	list, err := session.ListTools(context.Background(), &mcp.ListToolsParams{})
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range list.Tools {
		if strings.HasPrefix(tool.Name, "satchel_") {
			t.Errorf("agent can see management tool %q", tool.Name)
		}
	}
	for _, name := range []string{"satchel_list_modules", "satchel_set_module"} {
		result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: map[string]any{}})
		if err == nil && !result.IsError {
			t.Errorf("removed management tool %q executed", name)
		}
	}
}

func connectTestClient(t *testing.T, server *Server) *mcp.ClientSession {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(ctx, serverTransport) }()
	client := mcp.NewClient(&mcp.Implementation{Name: "satchel-test", Version: "0"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = session.Close()
		cancel()
		select {
		case <-serveDone:
		case <-time.After(time.Second):
			t.Error("MCP server did not stop")
		}
	})
	return session
}

func resultText(t *testing.T, result *mcp.CallToolResult) string {
	t.Helper()
	var b strings.Builder
	for _, content := range result.Content {
		if text, ok := content.(*mcp.TextContent); ok {
			b.WriteString(text.Text)
		}
	}
	return b.String()
}
