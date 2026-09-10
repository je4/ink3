package server

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/gin-gonic/gin"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func assertOutputSchemaObject(t *testing.T, tool *mcp.Tool) {
	t.Helper()
	if tool.OutputSchema == nil {
		t.Fatalf("tool %q has nil OutputSchema", tool.Name)
	}
	schemaMap, ok := tool.OutputSchema.(map[string]any)
	if !ok {
		raw, err := json.Marshal(tool.OutputSchema)
		if err != nil {
			t.Fatalf("tool %q failed to marshal OutputSchema: %v", tool.Name, err)
		}
		if err := json.Unmarshal(raw, &schemaMap); err != nil {
			t.Fatalf("tool %q failed to unmarshal OutputSchema to map: %v", tool.Name, err)
		}
	}
	if schemaMap["type"] != "object" {
		t.Errorf("tool %q expected OutputSchema type 'object', got %v", tool.Name, schemaMap["type"])
	}
}

func TestGetCollectionsTool(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()

	collections := []*CollFacetType{
		{
			Id:         1,
			Title:      "Test Collection 1",
			Identifier: "cat:\"test1\"",
			Url:        "https://example.com/1",
		},
		{
			Id:         2,
			Title:      "Test Collection 2",
			Identifier: "cat:\"test2\"",
			Url:        "https://example.com/2",
		},
	}

	ctrl := &Controller{
		name:        "test",
		collections: collections,
	}

	ctrl.initMCP(router)

	ts := httptest.NewServer(router)
	defer ts.Close()

	clientTransport := &mcp.StreamableClientTransport{
		Endpoint: ts.URL + "/mcp",
	}
	client := mcp.NewClient(&mcp.Implementation{
		Name:    "test client",
		Version: "0.0.1",
	}, nil)

	session, err := client.Connect(t.Context(), clientTransport, nil)
	if err != nil {
		t.Fatalf("failed to connect via StreamableHTTP: %v", err)
	}
	defer session.Close()

	toolsList, err := session.ListTools(t.Context(), &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("failed to list tools: %v", err)
	}

	var foundTool *mcp.Tool
	for _, tool := range toolsList.Tools {
		if tool.Name == "get_collections" {
			foundTool = tool
			break
		}
	}
	if foundTool == nil {
		t.Fatalf("tool 'get_collections' not found in tools list")
	}
	assertOutputSchemaObject(t, foundTool)

	res, err := session.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "get_collections",
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}

	if res.IsError {
		t.Fatalf("CallTool returned error")
	}

	raw, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatalf("failed to marshal structured content: %v", err)
	}
	var resultColls GetCollectionsResult
	if err := json.Unmarshal(raw, &resultColls); err != nil {
		t.Fatalf("failed to unmarshal structured content: %v", err)
	}

	if len(resultColls.Collections) != 2 {
		t.Fatalf("expected 2 collections, got %d", len(resultColls.Collections))
	}
	if resultColls.Collections[0].Title != "Test Collection 1" {
		t.Errorf("expected Title 'Test Collection 1', got '%s'", resultColls.Collections[0].Title)
	}
	if resultColls.Collections[1].Identifier != "cat:\"test2\"" {
		t.Errorf("expected Identifier 'cat:\"test2\"', got '%s'", resultColls.Collections[1].Identifier)
	}
}

func TestGetCollectionDescriptionTool(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()

	pagesFS := fstest.MapFS{
		"pages/coll1.md": &fstest.MapFile{
			Data: []byte(`---
type: collection
collectiontitle: Test Collection 1
title: Test Collection 1
---
Dies ist die Beschreibung der ersten Sammlung.`),
		},
		"pages/coll2.md": &fstest.MapFile{
			Data: []byte(`---
type: collection
collectiontitle: Test Collection 2
title: Test Collection 2
---
Dies ist die Beschreibung der zweiten Sammlung.`),
		},
	}

	collections := []*CollFacetType{
		{
			Id:    1,
			Title: "Test Collection 1",
		},
		{
			Id:    2,
			Title: "Test Collection 2",
		},
	}

	markdowns := map[string]map[string]string{
		"collection.test collection 1": {
			"type":            "collection",
			"collectiontitle": "Test Collection 1",
			"title":           "Test Collection 1",
			"path":            "pages/coll1.md",
		},
		"collection.test collection 2": {
			"type":            "collection",
			"collectiontitle": "Test Collection 2",
			"title":           "Test Collection 2",
			"path":            "pages/coll2.md",
		},
	}

	ctrl := &Controller{
		name:        "test",
		collections: collections,
		markdowns:   markdowns,
		pagesFS:     pagesFS,
	}

	ctrl.initMCP(router)

	ts := httptest.NewServer(router)
	defer ts.Close()

	clientTransport := &mcp.StreamableClientTransport{
		Endpoint: ts.URL + "/mcp",
	}
	client := mcp.NewClient(&mcp.Implementation{
		Name:    "test client",
		Version: "0.0.1",
	}, nil)

	session, err := client.Connect(t.Context(), clientTransport, nil)
	if err != nil {
		t.Fatalf("failed to connect via StreamableHTTP: %v", err)
	}
	defer session.Close()

	toolsList, err := session.ListTools(t.Context(), &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("failed to list tools: %v", err)
	}
	var foundTool *mcp.Tool
	for _, tool := range toolsList.Tools {
		if tool.Name == "get_collection_description" {
			foundTool = tool
			break
		}
	}
	if foundTool == nil {
		t.Fatalf("tool 'get_collection_description' not found in tools list")
	}
	assertOutputSchemaObject(t, foundTool)
	if !strings.Contains(foundTool.Description, "Markdown") {
		t.Errorf("expected tool description to mention Markdown, got %q", foundTool.Description)
	}

	// Test 1: Query by title
	res1, err := session.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "get_collection_description",
		Arguments: map[string]any{
			"title": "Test Collection 1",
		},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	if res1.IsError {
		t.Fatalf("CallTool returned error")
	}
	var desc1 GetCollectionDescriptionResult
	raw1, _ := json.Marshal(res1.StructuredContent)
	if err := json.Unmarshal(raw1, &desc1); err != nil {
		t.Fatalf("failed to unmarshal result: %v", err)
	}
	if desc1.Description != "Dies ist die Beschreibung der ersten Sammlung." {
		t.Errorf("expected description 'Dies ist die Beschreibung der ersten Sammlung.', got '%s'", desc1.Description)
	}
	if len(res1.Content) == 0 {
		t.Fatalf("expected at least 1 content item in res1")
	}
	embedded1, ok := res1.Content[0].(*mcp.EmbeddedResource)
	if !ok {
		t.Fatalf("expected Content[0] to be *mcp.EmbeddedResource, got %T", res1.Content[0])
	}
	if embedded1.Resource == nil {
		t.Fatalf("expected embedded1.Resource not to be nil")
	}
	if embedded1.Resource.MIMEType != "text/markdown" {
		t.Errorf("expected MIMEType 'text/markdown', got %q", embedded1.Resource.MIMEType)
	}
	if embedded1.Resource.URI != "collection://Test Collection 1" {
		t.Errorf("expected URI 'collection://Test Collection 1', got %q", embedded1.Resource.URI)
	}
	if embedded1.Resource.Text != "Dies ist die Beschreibung der ersten Sammlung." {
		t.Errorf("expected Resource.Text 'Dies ist die Beschreibung der ersten Sammlung.', got %q", embedded1.Resource.Text)
	}

	// Test 2: Query by id
	res2, err := session.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "get_collection_description",
		Arguments: map[string]any{
			"id": 2,
		},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	if res2.IsError {
		t.Fatalf("CallTool returned error")
	}
	var desc2 GetCollectionDescriptionResult
	raw2, _ := json.Marshal(res2.StructuredContent)
	if err := json.Unmarshal(raw2, &desc2); err != nil {
		t.Fatalf("failed to unmarshal result: %v", err)
	}
	if desc2.Description != "Dies ist die Beschreibung der zweiten Sammlung." {
		t.Errorf("expected description 'Dies ist die Beschreibung der zweiten Sammlung.', got '%s'", desc2.Description)
	}
	if len(res2.Content) == 0 {
		t.Fatalf("expected at least 1 content item in res2")
	}
	embedded2, ok := res2.Content[0].(*mcp.EmbeddedResource)
	if !ok {
		t.Fatalf("expected Content[0] to be *mcp.EmbeddedResource, got %T", res2.Content[0])
	}
	if embedded2.Resource == nil {
		t.Fatalf("expected embedded2.Resource not to be nil")
	}
	if embedded2.Resource.MIMEType != "text/markdown" {
		t.Errorf("expected MIMEType 'text/markdown', got %q", embedded2.Resource.MIMEType)
	}
	if embedded2.Resource.URI != "collection://2" {
		t.Errorf("expected URI 'collection://2', got %q", embedded2.Resource.URI)
	}
	if embedded2.Resource.Text != "Dies ist die Beschreibung der zweiten Sammlung." {
		t.Errorf("expected Resource.Text 'Dies ist die Beschreibung der zweiten Sammlung.', got %q", embedded2.Resource.Text)
	}

	// Test 3: Query with not found
	res3, err := session.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "get_collection_description",
		Arguments: map[string]any{
			"id": 999,
		},
	})
	if err != nil {
		t.Fatalf("CallTool returned unexpected transport error: %v", err)
	}
	if !res3.IsError {
		t.Fatalf("expected error result for nonexistent collection")
	}

	// Test 4: Query with empty arguments
	res4, err := session.CallTool(t.Context(), &mcp.CallToolParams{
		Name:      "get_collection_description",
		Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatalf("CallTool returned unexpected transport error: %v", err)
	}
	if !res4.IsError {
		t.Fatalf("expected error result for empty arguments")
	}
}

func TestGetEstatesTool(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()

	estates := []*CollFacetType{
		{
			Id:         10,
			Title:      "Test Estate 1",
			Identifier: "cat:\"estate1\"",
			Url:        "https://example.com/estate1",
		},
		{
			Id:         20,
			Title:      "Test Estate 2",
			Identifier: "cat:\"estate2\"",
			Url:        "https://example.com/estate2",
		},
	}

	ctrl := &Controller{
		name:    "test",
		estates: estates,
	}

	ctrl.initMCP(router)

	ts := httptest.NewServer(router)
	defer ts.Close()

	clientTransport := &mcp.StreamableClientTransport{
		Endpoint: ts.URL + "/mcp",
	}
	client := mcp.NewClient(&mcp.Implementation{
		Name:    "test client",
		Version: "0.0.1",
	}, nil)

	session, err := client.Connect(t.Context(), clientTransport, nil)
	if err != nil {
		t.Fatalf("failed to connect via StreamableHTTP: %v", err)
	}
	defer session.Close()

	toolsList, err := session.ListTools(t.Context(), &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("failed to list tools: %v", err)
	}

	var foundTool *mcp.Tool
	for _, tool := range toolsList.Tools {
		if tool.Name == "get_estates" {
			foundTool = tool
			break
		}
	}
	if foundTool == nil {
		t.Fatalf("tool 'get_estates' not found in tools list")
	}
	assertOutputSchemaObject(t, foundTool)

	res, err := session.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "get_estates",
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}

	if res.IsError {
		t.Fatalf("CallTool returned error")
	}

	raw, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatalf("failed to marshal structured content: %v", err)
	}
	var resultEstates GetEstatesResult
	if err := json.Unmarshal(raw, &resultEstates); err != nil {
		t.Fatalf("failed to unmarshal structured content: %v", err)
	}

	if len(resultEstates.Estates) != 2 {
		t.Fatalf("expected 2 estates, got %d", len(resultEstates.Estates))
	}
	if resultEstates.Estates[0].Title != "Test Estate 1" {
		t.Errorf("expected Title 'Test Estate 1', got '%s'", resultEstates.Estates[0].Title)
	}
	if resultEstates.Estates[1].Identifier != "cat:\"estate2\"" {
		t.Errorf("expected Identifier 'cat:\"estate2\"', got '%s'", resultEstates.Estates[1].Identifier)
	}
}

func TestGetEstateDescriptionTool(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()

	pagesFS := fstest.MapFS{
		"pages/estate1.md": &fstest.MapFile{
			Data: []byte(`---
type: estate
estatetitle: Test Estate 1
title: Test Estate 1
estateid: 10
---
Dies ist die Beschreibung des ersten Nachlasses.`),
		},
		"pages/estate2.md": &fstest.MapFile{
			Data: []byte(`---
type: estate
estatetitle: Test Estate 2
title: Test Estate 2
estateid: 20
---
Dies ist die Beschreibung des zweiten Nachlasses.`),
		},
		"pages/estate3.md": &fstest.MapFile{
			Data: []byte(`---
type: estate
estatetitle: Fallback Estate
description: Dies ist die Fallback-Beschreibung aus Meta.
---
`),
		},
	}

	estates := []*CollFacetType{
		{
			Id:    10,
			Title: "Test Estate 1",
		},
		{
			Id:    20,
			Title: "Test Estate 2",
		},
		{
			Id:    30,
			Title: "Fallback Estate",
		},
	}

	markdowns := map[string]map[string]string{
		"estate.test estate 1": {
			"type":        "estate",
			"estatetitle": "Test Estate 1",
			"title":       "Test Estate 1",
			"estateid":    "10",
			"path":        "pages/estate1.md",
		},
		"estate.test estate 2": {
			"type":        "estate",
			"estatetitle": "Test Estate 2",
			"title":       "Test Estate 2",
			"estateid":    "20",
			"path":        "pages/estate2.md",
		},
		"estate.fallback estate": {
			"type":        "estate",
			"estatetitle": "Fallback Estate",
			"title":       "Fallback Estate",
			"description": "Dies ist die Fallback-Beschreibung aus Meta.",
			"path":        "pages/estate3.md",
		},
	}

	ctrl := &Controller{
		name:      "test",
		estates:   estates,
		markdowns: markdowns,
		pagesFS:   pagesFS,
	}

	ctrl.initMCP(router)

	ts := httptest.NewServer(router)
	defer ts.Close()

	clientTransport := &mcp.StreamableClientTransport{
		Endpoint: ts.URL + "/mcp",
	}
	client := mcp.NewClient(&mcp.Implementation{
		Name:    "test client",
		Version: "0.0.1",
	}, nil)

	session, err := client.Connect(t.Context(), clientTransport, nil)
	if err != nil {
		t.Fatalf("failed to connect via StreamableHTTP: %v", err)
	}
	defer session.Close()

	toolsList, err := session.ListTools(t.Context(), &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("failed to list tools: %v", err)
	}
	var foundTool *mcp.Tool
	for _, tool := range toolsList.Tools {
		if tool.Name == "get_estate_description" {
			foundTool = tool
			break
		}
	}
	if foundTool == nil {
		t.Fatalf("tool 'get_estate_description' not found in tools list")
	}
	assertOutputSchemaObject(t, foundTool)
	if !strings.Contains(foundTool.Description, "Markdown") {
		t.Errorf("expected tool description to mention Markdown, got %q", foundTool.Description)
	}

	// Test 1: Query by title
	res1, err := session.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "get_estate_description",
		Arguments: map[string]any{
			"title": "Test Estate 1",
		},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	if res1.IsError {
		t.Fatalf("CallTool returned error")
	}
	var desc1 GetEstateDescriptionResult
	raw1, _ := json.Marshal(res1.StructuredContent)
	if err := json.Unmarshal(raw1, &desc1); err != nil {
		t.Fatalf("failed to unmarshal result: %v", err)
	}
	if desc1.Description != "Dies ist die Beschreibung des ersten Nachlasses." {
		t.Errorf("expected description 'Dies ist die Beschreibung des ersten Nachlasses.', got '%s'", desc1.Description)
	}
	if len(res1.Content) == 0 {
		t.Fatalf("expected at least 1 content item in res1")
	}
	embedded1, ok := res1.Content[0].(*mcp.EmbeddedResource)
	if !ok {
		t.Fatalf("expected Content[0] to be *mcp.EmbeddedResource, got %T", res1.Content[0])
	}
	if embedded1.Resource == nil {
		t.Fatalf("expected embedded1.Resource not to be nil")
	}
	if embedded1.Resource.MIMEType != "text/markdown" {
		t.Errorf("expected MIMEType 'text/markdown', got %q", embedded1.Resource.MIMEType)
	}
	if embedded1.Resource.URI != "estate://Test Estate 1" {
		t.Errorf("expected URI 'estate://Test Estate 1', got %q", embedded1.Resource.URI)
	}
	if embedded1.Resource.Text != "Dies ist die Beschreibung des ersten Nachlasses." {
		t.Errorf("expected Resource.Text 'Dies ist die Beschreibung des ersten Nachlasses.', got %q", embedded1.Resource.Text)
	}

	// Test 2: Query by id
	res2, err := session.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "get_estate_description",
		Arguments: map[string]any{
			"id": 20,
		},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	if res2.IsError {
		t.Fatalf("CallTool returned error")
	}
	var desc2 GetEstateDescriptionResult
	raw2, _ := json.Marshal(res2.StructuredContent)
	if err := json.Unmarshal(raw2, &desc2); err != nil {
		t.Fatalf("failed to unmarshal result: %v", err)
	}
	if desc2.Description != "Dies ist die Beschreibung des zweiten Nachlasses." {
		t.Errorf("expected description 'Dies ist die Beschreibung des zweiten Nachlasses.', got '%s'", desc2.Description)
	}
	if len(res2.Content) == 0 {
		t.Fatalf("expected at least 1 content item in res2")
	}
	embedded2, ok := res2.Content[0].(*mcp.EmbeddedResource)
	if !ok {
		t.Fatalf("expected Content[0] to be *mcp.EmbeddedResource, got %T", res2.Content[0])
	}
	if embedded2.Resource == nil {
		t.Fatalf("expected embedded2.Resource not to be nil")
	}
	if embedded2.Resource.MIMEType != "text/markdown" {
		t.Errorf("expected MIMEType 'text/markdown', got %q", embedded2.Resource.MIMEType)
	}
	if embedded2.Resource.URI != "estate://20" {
		t.Errorf("expected URI 'estate://20', got %q", embedded2.Resource.URI)
	}
	if embedded2.Resource.Text != "Dies ist die Beschreibung des zweiten Nachlasses." {
		t.Errorf("expected Resource.Text 'Dies ist die Beschreibung des zweiten Nachlasses.', got %q", embedded2.Resource.Text)
	}

	// Test 3: Query numeric string in title
	res3, err := session.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "get_estate_description",
		Arguments: map[string]any{
			"title": "20",
		},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	if res3.IsError {
		t.Fatalf("CallTool returned error: %v", res3)
	}
	var desc3 GetEstateDescriptionResult
	raw3, _ := json.Marshal(res3.StructuredContent)
	if err := json.Unmarshal(raw3, &desc3); err != nil {
		t.Fatalf("failed to unmarshal result: %v", err)
	}
	if desc3.Description != "Dies ist die Beschreibung des zweiten Nachlasses." {
		t.Errorf("expected description 'Dies ist die Beschreibung des zweiten Nachlasses.', got '%s'", desc3.Description)
	}

	// Test 4: Fallback to metadata description
	res4, err := session.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "get_estate_description",
		Arguments: map[string]any{
			"title": "Fallback Estate",
		},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	if res4.IsError {
		t.Fatalf("CallTool returned error")
	}
	var desc4 GetEstateDescriptionResult
	raw4, _ := json.Marshal(res4.StructuredContent)
	if err := json.Unmarshal(raw4, &desc4); err != nil {
		t.Fatalf("failed to unmarshal result: %v", err)
	}
	if desc4.Description != "Dies ist die Fallback-Beschreibung aus Meta." {
		t.Errorf("expected description 'Dies ist die Fallback-Beschreibung aus Meta.', got '%s'", desc4.Description)
	}

	// Test 5: Query with not found
	res5, err := session.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "get_estate_description",
		Arguments: map[string]any{
			"id": 999,
		},
	})
	if err != nil {
		t.Fatalf("CallTool returned unexpected transport error: %v", err)
	}
	if !res5.IsError {
		t.Fatalf("expected error result for nonexistent estate")
	}

	// Test 6: Query with empty arguments
	res6, err := session.CallTool(t.Context(), &mcp.CallToolParams{
		Name:      "get_estate_description",
		Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatalf("CallTool returned unexpected transport error: %v", err)
	}
	if !res6.IsError {
		t.Fatalf("expected error result for empty arguments")
	}
}

func TestControllerInitMarkdownIndexing(t *testing.T) {
	pagesFS := fstest.MapFS{
		"pages/estate1.md": &fstest.MapFile{
			Data: []byte(`---
type: estate
estatetitle: My Estate
---
Content of my estate`),
		},
		"pages/coll1.md": &fstest.MapFile{
			Data: []byte(`---
type: collection
collectiontitle: My Collection
---
Content of my collection`),
		},
		"pages/other.md": &fstest.MapFile{
			Data: []byte(`---
type: generic
title: Other Item
---
Content of other item`),
		},
	}

	ctrl := &Controller{
		name:       "test",
		pagesFS:    pagesFS,
		templateFS: fstest.MapFS{},
		staticFS:   fstest.MapFS{},
	}

	if err := ctrl.init(); err != nil {
		t.Fatalf("ctrl.init() failed: %v", err)
	}

	if len(ctrl.markdowns) != 3 {
		t.Fatalf("expected 3 indexed markdowns, got %d: %v", len(ctrl.markdowns), ctrl.markdowns)
	}

	if _, ok := ctrl.markdowns["estate.my estate"]; !ok {
		t.Errorf("expected key 'estate.my estate' in markdowns")
	}
	if _, ok := ctrl.markdowns["collection.my collection"]; !ok {
		t.Errorf("expected key 'collection.my collection' in markdowns")
	}
	if _, ok := ctrl.markdowns["generic.other item"]; !ok {
		t.Errorf("expected key 'generic.other item' in markdowns")
	}
}

func TestGetEstatesTool_FromMarkdowns(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()

	pagesFS := fstest.MapFS{
		"pages/estates/100_samuel_herzog.md": &fstest.MapFile{
			Data: []byte(`---
type: estate
estatetitle: Samuel Herzog
estateid: 100
identifier: 'cat:"herzog"'
url: /pages/estates/100_samuel_herzog
image: /images/herzog.jpg
contact: info@example.com
count: 42
---
Beschreibung Samuel Herzog`),
		},
		"pages/estates/200_anna_meier.md": &fstest.MapFile{
			Data: []byte(`---
type: estate
title: Anna Meier
id: 200
identifier: 'cat:"meier"'
---
Beschreibung Anna Meier`),
		},
		"pages/collections/85_poster.md": &fstest.MapFile{
			Data: []byte(`---
type: collection
collectiontitle: Plakatsammlung
id: 85
---
Beschreibung Plakate`),
		},
	}

	ctrl := &Controller{
		name:       "test",
		pagesFS:    pagesFS,
		templateFS: fstest.MapFS{},
		staticFS:   fstest.MapFS{},
		estates:    nil, // intentionally nil to test dynamic markdown discovery
	}

	if err := ctrl.init(); err != nil {
		t.Fatalf("ctrl.init() failed: %v", err)
	}

	ctrl.initMCP(router)

	ts := httptest.NewServer(router)
	defer ts.Close()

	clientTransport := &mcp.StreamableClientTransport{
		Endpoint: ts.URL + "/mcp",
	}
	client := mcp.NewClient(&mcp.Implementation{
		Name:    "test client",
		Version: "0.0.1",
	}, nil)

	session, err := client.Connect(t.Context(), clientTransport, nil)
	if err != nil {
		t.Fatalf("failed to connect via StreamableHTTP: %v", err)
	}
	defer session.Close()

	// 1. Call get_estates and verify dynamic population from markdowns
	res, err := session.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "get_estates",
	})
	if err != nil {
		t.Fatalf("CallTool get_estates failed: %v", err)
	}
	if res.IsError {
		t.Fatalf("CallTool get_estates returned error")
	}

	raw, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatalf("failed to marshal structured content: %v", err)
	}
	var resultEstates GetEstatesResult
	if err := json.Unmarshal(raw, &resultEstates); err != nil {
		t.Fatalf("failed to unmarshal structured content: %v", err)
	}

	if len(resultEstates.Estates) != 2 {
		t.Fatalf("expected 2 discovered estates, got %d", len(resultEstates.Estates))
	}
	if resultEstates.Estates[0].Id != 100 || resultEstates.Estates[0].Title != "Samuel Herzog" {
		t.Errorf("unexpected estate[0]: %+v", resultEstates.Estates[0])
	}
	if resultEstates.Estates[0].Identifier != `cat:"herzog"` || resultEstates.Estates[0].Url != "/pages/estates/100_samuel_herzog" || resultEstates.Estates[0].Contact != "info@example.com" || resultEstates.Estates[0].Count != 42 {
		t.Errorf("unexpected estate[0] metadata: %+v", resultEstates.Estates[0])
	}
	if resultEstates.Estates[1].Id != 200 || resultEstates.Estates[1].Title != "Anna Meier" {
		t.Errorf("unexpected estate[1]: %+v", resultEstates.Estates[1])
	}

	// 2. Call get_estate_description by ID on discovered estate
	descRes, err := session.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "get_estate_description",
		Arguments: map[string]any{
			"id": 100,
		},
	})
	if err != nil {
		t.Fatalf("CallTool get_estate_description failed: %v", err)
	}
	if descRes.IsError {
		t.Fatalf("CallTool get_estate_description returned error")
	}
	var desc GetEstateDescriptionResult
	rawDesc, _ := json.Marshal(descRes.StructuredContent)
	if err := json.Unmarshal(rawDesc, &desc); err != nil {
		t.Fatalf("failed to unmarshal description: %v", err)
	}
	if desc.Description != "Beschreibung Samuel Herzog" {
		t.Errorf("expected 'Beschreibung Samuel Herzog', got '%s'", desc.Description)
	}

	// 3. Call get_collections and verify dynamic population from markdowns
	collRes, err := session.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "get_collections",
	})
	if err != nil {
		t.Fatalf("CallTool get_collections failed: %v", err)
	}
	if collRes.IsError {
		t.Fatalf("CallTool get_collections returned error")
	}
	var resultCollections GetCollectionsResult
	rawColl, _ := json.Marshal(collRes.StructuredContent)
	if err := json.Unmarshal(rawColl, &resultCollections); err != nil {
		t.Fatalf("failed to unmarshal structured collections: %v", err)
	}
	if len(resultCollections.Collections) != 1 || resultCollections.Collections[0].Id != 85 || resultCollections.Collections[0].Title != "Plakatsammlung" {
		t.Errorf("unexpected collections: %+v", resultCollections.Collections)
	}
}

func TestMCPResourceTemplates(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()

	ctrl := &Controller{
		name: "test",
	}
	ctrl.initMCP(router)

	ts := httptest.NewServer(router)
	defer ts.Close()

	clientTransport := &mcp.StreamableClientTransport{
		Endpoint: ts.URL + "/mcp",
	}
	client := mcp.NewClient(&mcp.Implementation{
		Name:    "test client",
		Version: "0.0.1",
	}, nil)

	session, err := client.Connect(t.Context(), clientTransport, nil)
	if err != nil {
		t.Fatalf("failed to connect via StreamableHTTP: %v", err)
	}
	defer session.Close()

	templatesList, err := session.ListResourceTemplates(t.Context(), &mcp.ListResourceTemplatesParams{})
	if err != nil {
		t.Fatalf("failed to list resource templates: %v", err)
	}

	var foundColl, foundEstate bool
	for _, tpl := range templatesList.ResourceTemplates {
		if tpl.URITemplate == "collection://{title}" {
			foundColl = true
			if tpl.MIMEType != "text/markdown" {
				t.Errorf("expected collection template MIMEType 'text/markdown', got %q", tpl.MIMEType)
			}
			if tpl.Name != "collection_description" {
				t.Errorf("expected collection template Name 'collection_description', got %q", tpl.Name)
			}
		}
		if tpl.URITemplate == "estate://{title}" {
			foundEstate = true
			if tpl.MIMEType != "text/markdown" {
				t.Errorf("expected estate template MIMEType 'text/markdown', got %q", tpl.MIMEType)
			}
			if tpl.Name != "estate_description" {
				t.Errorf("expected estate template Name 'estate_description', got %q", tpl.Name)
			}
		}
	}

	if !foundColl {
		t.Errorf("resource template 'collection://{title}' not found")
	}
	if !foundEstate {
		t.Errorf("resource template 'estate://{title}' not found")
	}
}

func TestMCPResourcesRead(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()

	pagesFS := fstest.MapFS{
		"pages/coll1.md": &fstest.MapFile{
			Data: []byte(`---
type: collection
collectiontitle: Test Collection 1
title: Test Collection 1
---
Dies ist die Beschreibung der ersten Sammlung.`),
		},
		"pages/estate1.md": &fstest.MapFile{
			Data: []byte(`---
type: estate
estatetitle: Test Estate 1
title: Test Estate 1
estateid: 10
---
Dies ist die Beschreibung des ersten Nachlasses.`),
		},
	}

	collections := []*CollFacetType{
		{
			Id:    1,
			Title: "Test Collection 1",
		},
	}
	estates := []*CollFacetType{
		{
			Id:    10,
			Title: "Test Estate 1",
		},
	}

	markdowns := map[string]map[string]string{
		"collection.test collection 1": {
			"type":            "collection",
			"collectiontitle": "Test Collection 1",
			"title":           "Test Collection 1",
			"path":            "pages/coll1.md",
		},
		"estate.test estate 1": {
			"type":        "estate",
			"estatetitle": "Test Estate 1",
			"title":       "Test Estate 1",
			"estateid":    "10",
			"path":        "pages/estate1.md",
		},
	}

	ctrl := &Controller{
		name:        "test",
		collections: collections,
		estates:     estates,
		markdowns:   markdowns,
		pagesFS:     pagesFS,
	}

	ctrl.initMCP(router)

	ts := httptest.NewServer(router)
	defer ts.Close()

	clientTransport := &mcp.StreamableClientTransport{
		Endpoint: ts.URL + "/mcp",
	}
	client := mcp.NewClient(&mcp.Implementation{
		Name:    "test client",
		Version: "0.0.1",
	}, nil)

	session, err := client.Connect(t.Context(), clientTransport, nil)
	if err != nil {
		t.Fatalf("failed to connect via StreamableHTTP: %v", err)
	}
	defer session.Close()

	// 1. Read Collection with URL encoded title
	resColl, err := session.ReadResource(t.Context(), &mcp.ReadResourceParams{
		URI: "collection://Test%20Collection%201",
	})
	if err != nil {
		t.Fatalf("failed to read collection resource: %v", err)
	}
	if len(resColl.Contents) == 0 {
		t.Fatalf("expected contents in ReadResourceResult")
	}
	if resColl.Contents[0].MIMEType != "text/markdown" {
		t.Errorf("expected MIMEType 'text/markdown', got %q", resColl.Contents[0].MIMEType)
	}
	if resColl.Contents[0].Text != "Dies ist die Beschreibung der ersten Sammlung." {
		t.Errorf("expected text 'Dies ist die Beschreibung der ersten Sammlung.', got %q", resColl.Contents[0].Text)
	}

	// 2. Read Estate with URL encoded title
	resEstate, err := session.ReadResource(t.Context(), &mcp.ReadResourceParams{
		URI: "estate://Test%20Estate%201",
	})
	if err != nil {
		t.Fatalf("failed to read estate resource: %v", err)
	}
	if len(resEstate.Contents) == 0 {
		t.Fatalf("expected contents in ReadResourceResult")
	}
	if resEstate.Contents[0].MIMEType != "text/markdown" {
		t.Errorf("expected MIMEType 'text/markdown', got %q", resEstate.Contents[0].MIMEType)
	}
	if resEstate.Contents[0].Text != "Dies ist die Beschreibung des ersten Nachlasses." {
		t.Errorf("expected text 'Dies ist die Beschreibung des ersten Nachlasses.', got %q", resEstate.Contents[0].Text)
	}

	// 3. Read Non-existent Collection (expect error)
	_, err = session.ReadResource(t.Context(), &mcp.ReadResourceParams{
		URI: "collection://NonExistent",
	})
	if err == nil {
		t.Fatalf("expected error reading nonexistent collection, got nil")
	}

	// 4. Read Non-existent Estate (expect error)
	_, err = session.ReadResource(t.Context(), &mcp.ReadResourceParams{
		URI: "estate://NonExistent",
	})
	if err == nil {
		t.Fatalf("expected error reading nonexistent estate, got nil")
	}
}

func TestMCPToolsList_OutputSchema(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()

	ctrl := &Controller{
		name: "test",
	}
	ctrl.initMCP(router)

	ts := httptest.NewServer(router)
	defer ts.Close()

	clientTransport := &mcp.StreamableClientTransport{
		Endpoint: ts.URL + "/mcp",
	}
	client := mcp.NewClient(&mcp.Implementation{
		Name:    "test client",
		Version: "0.0.1",
	}, nil)

	session, err := client.Connect(t.Context(), clientTransport, nil)
	if err != nil {
		t.Fatalf("failed to connect via StreamableHTTP: %v", err)
	}
	defer session.Close()

	toolsList, err := session.ListTools(t.Context(), &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("failed to list tools: %v", err)
	}

	expectedTools := []string{
		"get_collections",
		"get_collection_description",
		"get_estates",
		"get_estate_description",
	}

	toolsMap := make(map[string]*mcp.Tool)
	for _, tool := range toolsList.Tools {
		toolsMap[tool.Name] = tool
	}

	for _, name := range expectedTools {
		tool, ok := toolsMap[name]
		if !ok {
			t.Errorf("tool %q missing from tools list", name)
			continue
		}
		assertOutputSchemaObject(t, tool)
	}
}
