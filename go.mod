module github.com/JoakimSoderberg/go-license-finder

go 1.14

require (
	github.com/dgryski/go-metro v0.0.0-20200812162917-85c65e2d0165 // indirect
	github.com/dgryski/go-minhash v0.0.0-20190315135803-ad340ca03076 // indirect
	github.com/dgryski/go-spooky v0.0.0-20170606183049-ed3d087f40e2 // indirect
	github.com/go-enry/go-license-detector/v4 v4.0.0
	github.com/montanaflynn/stats v0.6.3 // indirect
	github.com/neurosnap/sentences v1.0.6 // indirect
	github.com/shogo82148/go-shuffle v0.0.0-20180218125048-27e6095f230d // indirect
	github.com/shurcooL/sanitized_anchor_name v1.0.0 // indirect
	github.com/spf13/pflag v1.0.5
	gopkg.in/neurosnap/sentences.v1 v1.0.6 // indirect
	gopkg.in/yaml.v2 v2.2.4
)

// Used by cmd/license-finder.go
// We need our own version of this since the original project does not store the path to the license file
replace github.com/go-enry/go-license-detector/v4 => github.com/JoakimSoderberg/go-license-detector/v4 v4.0.0-20200827131053-a8ed0b9cb40a
