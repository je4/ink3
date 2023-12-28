package main

import (
	"bufio"
	"flag"
	"fmt"
	"github.com/gosimple/slug"
	"log"
	"os"
	"slices"
	"strings"
)

var fname = flag.String("f", "", "file to slugify")

func main() {
	flag.Parse()
	fp, err := os.Open(*fname)
	if err != nil {
		log.Fatal(err)
	}
	defer fp.Close()
	scanner := bufio.NewScanner(fp)
	vals := []string{}
	for scanner.Scan() {
		str := scanner.Text()
		str = strings.TrimSpace(str)
		if str == "" {
			continue
		}
		vals = append(vals, str)
	}
	slices.Sort(vals)
	vals = slices.Compact(vals)
	for _, str := range vals {
		fmt.Printf("voc_%s = \"%s\"\n", strings.Replace(slug.MakeLang(str, "de"), "-", "_", -1), str)
	}

	if err := scanner.Err(); err != nil {
		log.Fatal(err)
	}
}
