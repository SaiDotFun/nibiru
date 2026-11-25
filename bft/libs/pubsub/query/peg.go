package query

// Run the pinned generator from a stable relative path because its output
// header includes the invocation path. See https://github.com/pointlander/peg/issues/129.

//go:generate env GOBIN=$PWD/.bin go install github.com/pointlander/peg@v1.0.1
//go:generate ./.bin/peg -inline -switch query.peg
