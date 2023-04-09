module github.com/eh-steve/goloader/jit

go 1.18

require (
	github.com/bmatcuk/doublestar/v4 v4.4.0
	github.com/eh-steve/goloader v0.0.0-20230308033449-b10260b5928a
	github.com/eh-steve/goloader/jit/testdata v0.0.0-20230308033449-b10260b5928a
	golang.org/x/tools v0.2.0
)

require (
	github.com/opentracing/opentracing-go v1.2.0 // indirect
	golang.org/x/mod v0.6.0 // indirect
	golang.org/x/sys v0.1.0 // indirect
)

replace github.com/eh-steve/goloader => ../

replace github.com/eh-steve/goloader/jit/testdata => ./testdata
