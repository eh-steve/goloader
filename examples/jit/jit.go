package main

import (
	"github.com/pkujhd/goloader/examples/issue55/p"
	"github.com/pkujhd/goloader/jit"
)

func main() {
	buildConfig := jit.BuildConfig{
		KeepTempFiles:   false,
		ExtraBuildFlags: nil,
		BuildEnv:        nil,
		TmpDir:          "",
		DebugLog:        true,
	}
	loadable, err := jit.BuildGoPackage(buildConfig, "../issue55/t/")
	if err != nil {
		panic(err)
	}

	module, symbols, err := loadable.Load()
	if err != nil {
		panic(err)
	}
	Test := symbols["Test"].(func(p.Intf) p.Intf)
	Test(&p.Stru{})
	err = module.Unload()
	if err != nil {
		panic(err)
	}
}
