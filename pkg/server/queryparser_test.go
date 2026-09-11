package server

import (
	"reflect"
	"testing"
)

func TestParseQuery(t *testing.T) {
	tests := []struct {
		name          string
		query         string
		wantFilter    map[string]string
		wantQuery     string
		expectNoError bool
	}{
		{
			name:       "legacy query with properties and phrases",
			query:      "author:\"John Doe\" test  \"lorem ipsum dolor\"  test:\"blubb\" hello world",
			wantFilter: map[string]string{"author": "John Doe", "test": "blubb"},
			wantQuery:  "test \"lorem ipsum dolor\" hello world",
		},
		{
			name:       "single term",
			query:      "performance",
			wantFilter: map[string]string{},
			wantQuery:  "performance",
		},
		{
			name:       "empty query",
			query:      "   ",
			wantFilter: map[string]string{},
			wantQuery:  "",
		},
		{
			name:       "boolean AND and NOT",
			query:      "\"Performance\" AND NOT \"Video\"",
			wantFilter: map[string]string{},
			wantQuery:  "\"Performance\" + -\"Video\"",
		},
		{
			name:       "boolean OR",
			query:      "Basel OR Zurich",
			wantFilter: map[string]string{},
			wantQuery:  "Basel | Zurich",
		},
		{
			name:       "lowercase boolean operators",
			query:      "performance and not video",
			wantFilter: map[string]string{},
			wantQuery:  "performance + -video",
		},
		{
			name:       "grouped expressions with boolean operators",
			query:      "(Performance OR Concert) AND NOT Basel",
			wantFilter: map[string]string{},
			wantQuery:  "(Performance | Concert) + -Basel",
		},
		{
			name:       "nested grouped expressions",
			query:      "((Performance OR Concert) AND Basel) OR Zurich",
			wantFilter: map[string]string{},
			wantQuery:  "((Performance | Concert) + Basel) | Zurich",
		},
		{
			name:       "symbol operators &&, ||, !",
			query:      "dance && !music || video",
			wantFilter: map[string]string{},
			wantQuery:  "dance + -music | video",
		},
		{
			name:       "prefix operators + and -",
			query:      "+Performance -Video",
			wantFilter: map[string]string{},
			wantQuery:  "+Performance -Video",
		},
		{
			name:       "field filter with boolean grouped query",
			query:      "author:\"John Doe\" AND (dance OR music)",
			wantFilter: map[string]string{"author": "John Doe"},
			wantQuery:  "(dance | music)",
		},
		{
			name:       "field filter with unquoted value and hyphen",
			query:      "author:Basel-Stadt AND video",
			wantFilter: map[string]string{"author": "Basel-Stadt"},
			wantQuery:  "video",
		},
		{
			name:       "field filter at end with AND",
			query:      "performance AND author:\"John Doe\"",
			wantFilter: map[string]string{"author": "John Doe"},
			wantQuery:  "performance",
		},
		{
			name:       "field filter between terms with AND",
			query:      "performance AND author:\"John Doe\" AND video",
			wantFilter: map[string]string{"author": "John Doe"},
			wantQuery:  "performance + video",
		},
		{
			name:       "multiple field filters only",
			query:      "author:\"John Doe\" title:Video status:active",
			wantFilter: map[string]string{"author": "John Doe", "title": "Video", "status": "active"},
			wantQuery:  "",
		},
		{
			name:       "field filter with NOT",
			query:      "author:Basel AND NOT \"Video\"",
			wantFilter: map[string]string{"author": "Basel"},
			wantQuery:  "-\"Video\"",
		},
		{
			name:       "syntax anomaly unbalanced parenthesis fallback",
			query:      "author:\"John Doe\" (dance OR music",
			wantFilter: map[string]string{"author": "John Doe"},
			wantQuery:  "(dance OR music",
		},
		{
			name:       "syntax anomaly trailing colon fallback",
			query:      "author:\"Jane\" unknown:",
			wantFilter: map[string]string{"author": "Jane"},
			wantQuery:  "unknown:",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filter, qStr, err := parseQuery(tt.query)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(filter, tt.wantFilter) {
				t.Errorf("filter mismatch\ngot:  %+v\nwant: %+v", filter, tt.wantFilter)
			}
			if qStr != tt.wantQuery {
				t.Errorf("query string mismatch\ngot:  %q\nwant: %q", qStr, tt.wantQuery)
			}
		})
	}
}
