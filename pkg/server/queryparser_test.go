package server

import "testing"
import "github.com/alecthomas/repr"

func TestParseQuery(t *testing.T) {
	query := "author:\"John Doe\" test  \"lorem ipsum dolor\"  test:\"blubb\" hello world"
	filter, qStr, err := parseQuery(query)
	if err != nil {
		t.Fatal(err)
	}
	repr.Println(filter)
	repr.Println(qStr)
	//t.Logf("result: %v", result)
}
