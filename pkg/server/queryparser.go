package server

import (
	"emperror.dev/errors"
	"github.com/alecthomas/participle/v2"
	"strings"
)

type Property struct {
	Key   string `@Ident ":"`
	Value string `@String | @Ident`
}

func (Property) all() {}

type String struct {
	Value string `@String`
}

func (String) all() {}

type All interface {
	all()
}

type Query struct {
	Items []All `( @@ " "?)*`
}

var queryPparser = participle.MustBuild[Query](
	//participle.Unquote("String"),
	participle.Union[All](String{}, Property{}),
)

func parseQuery(query string) (map[string]string, string, error) {

	result, err := queryPparser.ParseString("", query)
	if err != nil {
		return nil, "", errors.Wrapf(err, "cannot parse query '%s'", query)
	}

	filter := map[string]string{}
	qResult := ""
	for _, item := range result.Items {
		switch i := item.(type) {
		case Property:
			if i.Key != "" {
				filter[i.Key] = i.Value
			} else {
				qResult += " " + i.Value
			}
		case String:
			qResult += " " + i.Value
		default:
			return nil, "", errors.Errorf("unknown item type %T", item)
		}
	}
	return filter, strings.TrimSpace(qResult), nil
}
