// Command cvdump prints what a CV file parses into. Development aid for
// checking a real file before wiring it into the upload endpoint.
package main

import (
	"encoding/json"
	"fmt"
	"os"

	"jobpilot/core/api/internal/cv"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("usage: cvdump <file> [--text]")
		os.Exit(2)
	}
	data, err := os.ReadFile(os.Args[1])
	if err != nil {
		fmt.Println("ERROR:", err)
		os.Exit(1)
	}
	text, format, err := cv.ExtractText(os.Args[1], data)
	if err != nil {
		fmt.Println("ERROR:", err)
		os.Exit(1)
	}
	if len(os.Args) > 2 && os.Args[2] == "--text" {
		fmt.Printf("format=%s chars=%d\n----\n%s\n", format, len(text), text)
		return
	}
	parsed := cv.Parse(text)
	parsed.Text = ""
	out, _ := json.MarshalIndent(parsed, "", "  ")
	fmt.Printf("format=%s\n%s\n", format, out)
}
