package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/pflag"

	"github.com/go-enry/go-license-detector/v4/licensedb"
)

const exampleInput = `{
	"Path": "gopkg.in/yaml.v2",
	"Version": "v2.2.2",
	"Time": "2018-11-15T11:05:04Z",
	"Update": {
		"Path": "gopkg.in/yaml.v2",
		"Version": "v2.3.0",
		"Time": "2020-05-06T23:08:38Z"
	},
	"Dir": "/home/js/go/pkg/mod/gopkg.in/yaml.v2@v2.2.2",
	"GoMod": "/home/js/go/pkg/mod/cache/download/gopkg.in/yaml.v2/@v/v2.2.2.mod"
}`

const usage = `This program will find the license of one or more given dependencies
By default it will listen to stdin`

// License represents a found license
type License struct {
	Name       string  `json:"Name"`
	Path       string  `json:"Path"`
	Contents   string  `json:"Contents"`
	Confidence float32 `json:"Confidence"`
	Error      string  `json:"Error"`
}

// Dependency is a struct representing a Go mod dependency as returned
// by the `go list` tool when called with these parameters:
// `go list -m -u -json <path to dependency>`
type Dependency struct {
	Path    string    `json:"Path"`
	Version string    `json:"Version"`
	Time    time.Time `json:"Time"`
	Update  struct {
		Path    string    `json:"Path"`
		Version string    `json:"Version"`
		Time    time.Time `json:"Time"`
	} `json:"Update"`
	Dir     string  `json:"Dir"`
	GoMod   string  `json:"GoMod"`
	License License `json:"License"`
}

var verbose bool
var depTimeout time.Duration

func printProgress(format string, args ...interface{}) {
	if verbose {
		fmt.Fprintf(os.Stderr, format, args...)
	}
}

func main() {
	// gobindep build/ncc3-agent-reg-service | awk '{ print $1"@"$2 }' | xargs go list -m -u -json | ./lic | jq '{ name: .Path, version: .Version, license: .License.Name, Contents: .License.File}'

	//input := os.Args[1]
	// TODO: Argument with extra file that takes uknown licenses
	var inputFile string
	pflag.Usage = func() {
		fmt.Fprintf(os.Stderr, `
License finder

This program will find the license of one or more given dependencies.
The input format expected is from running this command:

  go list -m -u -json <file path to dependency>

Example:

%s

If no input file is specified, the program will read the JSON from stdin.
Multiple JSON objects is allowed, no special delimeter is required
as long as the input is valid JSON.

Usage:
  %s [flags]

Flags:
`, exampleInput, os.Args[0])
		pflag.PrintDefaults()
	}
	pflag.StringVarP(&inputFile, "input-file", "i", "", "Input filename containing JSON to read")
	pflag.BoolVarP(&verbose, "verbose", "v", false, "Adds extra log messages")
	pflag.DurationVar(&depTimeout, "dependency-timeout", time.Duration(5*time.Second), "Timeout for finding the license for each dependency")
	pflag.Parse()

	var f io.Reader

	if inputFile != "" {
		printProgress("Reading from file %s...", inputFile)

		var err error
		f, err = os.Open(inputFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to open input file %s: %s\n", inputFile, err)
			os.Exit(5)
		}
	} else {
		printProgress("Reading from stdin...")
		f = os.Stdin
	}

	// This reads JSON from the input, multiple objects following each other is allowed.
	dec := json.NewDecoder(bufio.NewReader(f))
	for {
		var dep Dependency
		if err := dec.Decode(&dep); err == io.EOF {
			printProgress("EOF")
			break
		} else if err != nil {
			log.Fatal(err)
		}
		GetDependencyLicense(dep)
	}
}

// GetDependencyLicense tries to figure out the license for a given dependency.
func GetDependencyLicense(dep Dependency) {

	ch := make(chan []licensedb.Result, 1)
	go func() {
		// Figure out what license this dependency has.
		results := licensedb.Analyse(dep.Dir)
		ch <- results
	}()

	var results []licensedb.Result

	select {
	case results = <-ch:
	case <-time.After(depTimeout):
		log.Fatalf("Timed out after %v trying to get the license for: '%s'", depTimeout, dep.Path)
	}

	// Since we only pass a single directory we expect only one result
	if len(results) != 1 {
		fmt.Fprintf(os.Stderr, "Expected a single result for %s but got %d", dep.Dir, len(results))
		os.Exit(4)
	}

	result := results[0]
	output := dep
	output.License.Error = result.ErrStr

	if len(result.Matches) > 0 {
		match := result.Matches[0]
		output.License = License{
			Name:       match.License,
			Path:       filepath.Join(dep.Dir, match.File),
			Confidence: match.Confidence,
		}

		b, err := ioutil.ReadFile(output.License.Path)
		if err != nil {
			output.License.Error = fmt.Sprintf("Failed to open license file: %s", err.Error())
		}
		output.License.Contents = string(b)
	}

	bytes, err := json.Marshal(output)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to marshal JSON: %s\n", err)
		os.Exit(3)
	}

	fmt.Println(string(bytes))
}
