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
	"gopkg.in/yaml.v2"
)

// TODO: Add support for google/go-licenses also
// TODO: Move all but cli parts into separate package
// TODO: Add tests
// TODO: Get rid of dependency to go-license-detector
// TODO: Add support for separating each part of the scan
//       for example, finding the potential license files

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
var globalTimeout time.Duration
var errorIsFatal bool
var includeLicenseContents bool
var knownLicensePath string

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
	pflag.DurationVar(&globalTimeout, "timeout", time.Duration(5*time.Minute), "Global timeout for finding the license for all dependencies")
	pflag.BoolVarP(&errorIsFatal, "error-is-fatal", "e", false, "Exit fatally on any type of error, for example if the license is not found for a dependency. Default is to just store the Error in the output")
	pflag.BoolVar(&includeLicenseContents, "include-license-contents", true, "Set to false to exclude the contents of the License file")
	pflag.StringVarP(&knownLicensePath, "known-licenses-config", "k", "", "Path to a file containng a map of known licenses in JSON/YAML. Key should be Path@Version. This is checked first before searching for the license")

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

	ch := make(chan struct{}, 1)
	go func() {

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

		ch <- struct{}{}
	}()

	select {
	case _ = <-ch:
	case <-time.After(globalTimeout):
		log.Fatalf("Global timeout elapsed after %s trying to get the licenses for", globalTimeout)
	}
}

type KnownLicense struct {
	Name string `yaml:"Name"` // Name of the License according to https://spdx.org/licenses/
	Path string `yaml:"Path"`
}

type KnownLicenses struct {
	Licenses map[string]KnownLicense `yaml:licenses`
}

func readKnownLicenses(path string) (*KnownLicenses, error) {
	content, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var known KnownLicenses

	err = yaml.Unmarshal(content, &known)
	if err != nil {
		return nil, err
	}

	// TODO: Add verification of License names according to https://spdx.org/licenses/

	return &known, nil
}

type AnalyzeSummary struct {
	Result             licensedb.Result
	LeavePathUntouched bool // Should we touch the final License path or not?
}

// GetDependencyLicense tries to figure out the license for a given dependency.
func GetDependencyLicense(dep Dependency) {

	ch := make(chan AnalyzeSummary, 1)
	go func() {
		// TODO: Break out into function
		if knownLicensePath != "" {
			log.Printf("Opening config file for Known licenses:\n  %s", knownLicensePath)
			known, err := readKnownLicenses(knownLicensePath)
			if err != nil {
				log.Fatalf("Failed to read known license config file: %s\n", err)
			}

			if len(known.Licenses) == 0 {
				log.Fatalf("%s contained to licenses! Did you put them under \"licenses:\"?", knownLicensePath)
			}

			for k := range known.Licenses {
				log.Println(k)
			}

			for _, name := range []string{dep.Path + "@" + dep.Version, dep.Path} {
				log.Println("Looking for ", name)
				if knownLicense, ok := known.Licenses[name]; ok {
					log.Printf("  Found known license entry for %s\n", name)
					summary := AnalyzeSummary{
						Result: licensedb.Result{
							Arg:    dep.Dir,
							ErrStr: "",
							Matches: []licensedb.Match{
								{
									File:       knownLicense.Path,
									Confidence: 1.0,
									License:    knownLicense.Name,
								},
							},
						},
						// We provide the Path for the License in the
						// known licenses config, so we should not touch it.
						LeavePathUntouched: true,
					}

					ch <- summary
					return
				}
			}
		}

		results := licensedb.Analyse(dep.Dir)

		// Since we only pass a single directory we expect only one result
		if len(results) != 1 {
			log.Fatalf("Expected a single result for %s but got %d", dep.Dir, len(results))
		}

		// Figure out what license this dependency has.
		summary := AnalyzeSummary{
			Result: results[0],
			// We should have found the path to the license automatically,
			// relative to the source directory for the dependency.
			LeavePathUntouched: false,
		}
		ch <- summary
	}()

	var summary AnalyzeSummary

	select {
	case summary = <-ch:
	case <-time.After(depTimeout):
		log.Fatalf("Timed out after %v trying to get the license for: '%s'", depTimeout, dep.Path)
	}

	output := dep
	output.License.Error = summary.Result.ErrStr

	if len(summary.Result.Matches) > 0 {
		match := summary.Result.Matches[0]
		licensePath := match.File
		if !summary.LeavePathUntouched {
			licensePath = filepath.Join(output.Dir, match.File)
		}

		output.License = License{
			Name:       match.License,
			Path:       licensePath,
			Confidence: match.Confidence,
		}

		if includeLicenseContents {
			b, err := ioutil.ReadFile(output.License.Path)
			if err != nil {
				output.License.Error = fmt.Sprintf("Failed to open license file: %s", err.Error())
			}
			output.License.Contents = string(b)
		}
	}

	bytes, err := json.Marshal(output)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to marshal JSON: %s\n", err)
		os.Exit(3)
	}

	fmt.Println(string(bytes))

	if errorIsFatal && output.License.Error != "" {
		log.Fatalf("Fatal error for \"%s\": %s", dep.Path, output.License.Error)
	}
}
