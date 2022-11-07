module github.com/pkujhd/goloader/examples

go 1.19

require (
	github.com/pkujhd/goloader v0.0.0-20221026112716-fed0a9e75321
	github.com/pkujhd/goloader/jit v0.0.0-00010101000000-000000000000
)

require golang.org/x/sys v0.1.0 // indirect

replace (
	github.com/pkujhd/goloader => ../
	github.com/pkujhd/goloader/jit => ../jit
	github.com/pkujhd/goloader/jit/testdata => ../jit/testdata
)
