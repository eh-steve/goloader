package jit_test

import (
	"bytes"
	"crypto/x509"
	"fmt"
	"github.com/pkujhd/goloader"
	"github.com/pkujhd/goloader/jit"
	"github.com/pkujhd/goloader/jit/testdata/common"
	"github.com/pkujhd/goloader/jit/testdata/test_issue55/p"
	"net"
	"net/http"
	"os"
	"runtime"
	"runtime/debug"
	"sort"
	"sync"
	"testing"
	"time"
	"unsafe"
)

// Can edit these flags to check all tests still work with different linker options
var heapStrings = false
var stringContainerSize = 0 // 512 * 1024
//var goBinary = "/mnt/rpool/go_versions/go1.18.8.linux-amd64/go/bin/go"

var goBinary = ""

type testData struct {
	files []string
	pkg   string
}

func buildLoadable(t *testing.T, conf jit.BuildConfig, testName string, data testData) (module *goloader.CodeModule, symbols map[string]interface{}) {
	var loadable *jit.LoadableUnit
	var err error
	switch testName {
	case "BuildGoFiles":
		loadable, err = jit.BuildGoFiles(conf, data.files[0], data.files[1:]...)
	case "BuildGoPackage":
		loadable, err = jit.BuildGoPackage(conf, data.pkg)
	case "BuildGoText":
		var goText []byte
		goText, err = os.ReadFile(data.files[0])
		if err != nil {
			t.Fatal(err)
		}
		loadable, err = jit.BuildGoText(conf, string(goText))
	}
	if err != nil {
		t.Fatal(err)
	}
	module, symbols, err = loadable.Load()
	if err != nil {
		t.Fatal(err)
	}
	return
}

func TestJitSimpleFunctions(t *testing.T) {
	conf := jit.BuildConfig{
		GoBinary:            goBinary,
		KeepTempFiles:       false,
		ExtraBuildFlags:     nil,
		BuildEnv:            nil,
		TmpDir:              "",
		DebugLog:            false,
		HeapStrings:         heapStrings,
		StringContainerSize: stringContainerSize,
	}

	data := testData{
		files: []string{"./testdata/test_simple_func/test.go"},
		pkg:   "./testdata/test_simple_func",
	}
	testNames := []string{"BuildGoFiles", "BuildGoPackage", "BuildGoText"}

	for _, testName := range testNames {
		t.Run(testName, func(t *testing.T) {
			module, symbols := buildLoadable(t, conf, testName, data)

			addFunc := symbols["Add"].(func(a, b int) int)
			result := addFunc(5, 6)
			if result != 11 {
				t.Errorf("expected %d, got %d", 11, result)
			}

			handleBytesFunc := symbols["HandleBytes"].(func(input interface{}) ([]byte, error))
			bytesOut, err := handleBytesFunc([]byte{1, 2, 3})
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(bytesOut, []byte{1, 2, 3}) {
				t.Errorf("expected %v, got %v", []byte{1, 2, 3}, bytesOut)
			}
			err = module.Unload()
			if err != nil {
				t.Fatal(err)
			}
			err = module.UnloadStringMap()
		})
	}
}

func TestJitJsonUnmarshal(t *testing.T) {
	conf := jit.BuildConfig{
		GoBinary:            goBinary,
		KeepTempFiles:       false,
		ExtraBuildFlags:     nil,
		BuildEnv:            nil,
		TmpDir:              "",
		DebugLog:            false,
		HeapStrings:         heapStrings,
		StringContainerSize: stringContainerSize,
	}

	data := testData{
		files: []string{"./testdata/test_json_unmarshal/test.go"},
		pkg:   "./testdata/test_json_unmarshal",
	}
	testNames := []string{"BuildGoFiles", "BuildGoPackage", "BuildGoText"}

	for _, testName := range testNames {
		t.Run(testName, func(t *testing.T) {
			module, symbols := buildLoadable(t, conf, testName, data)

			MyFunc := symbols["MyFunc"].(func([]byte) (interface{}, error))
			result, err := MyFunc([]byte(`{"key": "value"}`))
			if err != nil {
				t.Fatal(err)
			}
			if result.(map[string]interface{})["key"] != "value" {
				t.Errorf("expected %s, got %v", "value", result)
			}

			err = module.Unload()
			if err != nil {
				t.Fatal(err)
			}
			if err != nil {
				t.Fatal(err)
			}
			err = module.UnloadStringMap()
		})
	}
}

func TestJitComplexFunctions(t *testing.T) {
	conf := jit.BuildConfig{
		KeepTempFiles:   false,
		ExtraBuildFlags: nil,
		BuildEnv:        nil,
		TmpDir:          "",
		DebugLog:        true,
	}

	data := testData{
		files: []string{"./testdata/test_complex_func/test.go"},
		pkg:   "testdata/test_complex_func",
	}
	testNames := []string{"BuildGoFiles", "BuildGoPackage", "BuildGoText"}

	for _, testName := range testNames {
		t.Run(testName, func(t *testing.T) {
			module, symbols := buildLoadable(t, conf, testName, data)
			complexFunc := symbols["ComplexFunc"].(func(input common.SomeStruct) (common.SomeStruct, error))
			result, err := complexFunc(common.SomeStruct{
				Val1:  []byte{1, 2, 3},
				Mutex: &sync.Mutex{},
			})
			if err != nil {
				t.Fatal(err)
			}

			if !bytes.Equal(result.Val1.([]byte), []byte{3, 2, 1}) {
				t.Errorf("expected %d, got %d", []byte{3, 2, 1}, result.Val1)
			}

			newThingFunc := symbols["NewThing"].(func() common.SomeInterface)

			thing := newThingFunc()
			err = thing.Method2(map[string]interface{}{
				"item1": 5,
				"item2": 6,
			})
			if err != nil {
				t.Fatal(err)
			}
			result, err = thing.Method1(common.SomeStruct{
				Val1:  []byte{1, 2, 3},
				Mutex: &sync.Mutex{},
			})
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(result.Val1.([]byte), []byte{3, 2, 1}) {
				t.Errorf("expected %d, got %d", []byte{3, 2, 1}, result.Val1)
			}
			if result.Val2["item1"].(int) != 5 {
				t.Errorf("expected %d, got %d", 5, result.Val2["item1"])
			}
			if result.Val2["item2"].(int) != 6 {
				t.Errorf("expected %d, got %d", 6, result.Val2["item2"])
			}

			err = module.Unload()
			if err != nil {
				t.Fatal(err)
			}
			if err != nil {
				t.Fatal(err)
			}
			err = module.UnloadStringMap()
		})
	}
}

func TestJitEmbeddedStruct(t *testing.T) {
	conf := jit.BuildConfig{
		GoBinary:            goBinary,
		KeepTempFiles:       false,
		ExtraBuildFlags:     nil,
		BuildEnv:            nil,
		TmpDir:              "",
		DebugLog:            false,
		HeapStrings:         heapStrings,
		StringContainerSize: stringContainerSize,
	}

	data := testData{
		files: []string{"./testdata/test_embedded/test.go"},
		pkg:   "testdata/test_embedded",
	}
	testNames := []string{"BuildGoFiles", "BuildGoPackage", "BuildGoText"}

	for _, testName := range testNames {
		t.Run(testName, func(t *testing.T) {
			module, symbols := buildLoadable(t, conf, testName, data)

			makeIt := symbols["MakeIt"].(func() int)
			result := makeIt()
			if result != 5 {
				t.Fatalf("expected 5, got %d", result)
			}

			err := module.Unload()
			if err != nil {
				t.Fatal(err)
			}
			if err != nil {
				t.Fatal(err)
			}
			err = module.UnloadStringMap()
		})
	}
}

func TestJitCGoCall(t *testing.T) {
	conf := jit.BuildConfig{
		GoBinary:            goBinary,
		KeepTempFiles:       false,
		ExtraBuildFlags:     nil,
		BuildEnv:            nil,
		TmpDir:              "",
		DebugLog:            false,
		HeapStrings:         heapStrings,
		StringContainerSize: stringContainerSize,
	}

	data := testData{
		files: []string{"./testdata/test_cgo/test.go"},
		pkg:   "testdata/test_cgo",
	}
	testNames := []string{"BuildGoFiles", "BuildGoPackage", "BuildGoText"}

	for _, testName := range testNames {
		t.Run(testName, func(t *testing.T) {
			module, symbols := buildLoadable(t, conf, testName, data)
			cgoCall := symbols["CGoCall"].(func(a, b int32) (int32, int32, int32))
			mul, add, constant := cgoCall(2, 3)

			// This won't pass since nothing currently applies native elf/macho relocations in native code
			if false {
				if mul != 6 {
					t.Errorf("expected mul to be 2 * 3 == 6, got %d", mul)
				}
				if add != 6 {
					t.Errorf("expected mul to be 2 + 3 == 5, got %d", add)
				}
				if constant != 5 {
					t.Errorf("expected constant to be 5, got %d", add)
				}
			}

			fmt.Println(mul, add, constant)

			err := module.Unload()
			if err != nil {
				t.Fatal(err)
			}
			if err != nil {
				t.Fatal(err)
			}
			err = module.UnloadStringMap()
		})
	}
}

func TestJitHttpGet(t *testing.T) {
	conf := jit.BuildConfig{
		GoBinary:            goBinary,
		KeepTempFiles:       false,
		ExtraBuildFlags:     nil,
		BuildEnv:            nil,
		TmpDir:              "",
		DebugLog:            false,
		HeapStrings:         heapStrings,
		StringContainerSize: stringContainerSize,
	}

	data := testData{
		files: []string{"./testdata/test_http_get/test.go"},
		pkg:   "testdata/test_http_get",
	}
	testNames := []string{"BuildGoFiles", "BuildGoPackage", "BuildGoText"}

	// Certain crypto and http library code has asynchronous execution of various functions
	// (for caching things after first computation using sync.Once etc.) e.g.:
	// vendor/golang.org/x/net/http/httpproxy.(*config).proxyForURL
	// crypto/x509.initSystemRoots
	// To avoid these functions running against unloaded memory after each test is finished, we build them into the test binary
	// (but don't build all of net/http in, so it's still a useful test)

	r, _ := http.NewRequest("", "", nil)
	_, _ = http.ProxyFromEnvironment(r)
	_, _ = x509.SystemCertPool()
	x509.NewCertPool().AppendCertsFromPEM(nil)

	for _, testName := range testNames {
		t.Run(testName, func(t *testing.T) {
			start := runtime.NumGoroutine()
			module, symbols := buildLoadable(t, conf, testName, data)
			httpGet := symbols["MakeHTTPRequestWithDNS"].(func(string) (string, error))
			result, err := httpGet("https://ipinfo.io/ip")
			if err != nil {
				t.Fatal(err)
			}
			afterCall := runtime.NumGoroutine()
			for afterCall != start {
				time.Sleep(100 * time.Millisecond)
				runtime.GC()
				afterCall = runtime.NumGoroutine()
				fmt.Printf("Waiting for last goroutine to stop before unloading, started with %d, now have %d\n", start, afterCall)
			}
			fmt.Println(result)
			err = module.Unload()
			if err != nil {
				t.Fatal(err)
			}
			err = module.UnloadStringMap()
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

// TODO - something wrong with this
func TestJitPanicRecoveryStackTrace(t *testing.T) {
	conf := jit.BuildConfig{
		GoBinary:            goBinary,
		KeepTempFiles:       false,
		ExtraBuildFlags:     nil,
		BuildEnv:            nil,
		TmpDir:              "",
		DebugLog:            false,
		HeapStrings:         heapStrings,
		StringContainerSize: stringContainerSize,
	}

	data := testData{
		files: []string{"./testdata/test_stack_trace/file1.go",
			"./testdata/test_stack_trace/file2.go",
			"./testdata/test_stack_trace/file3.go",
			"./testdata/test_stack_trace/file4.go",
			"./testdata/test_stack_trace/test.go"},
		pkg: "testdata/test_stack_trace",
	}
	testNames := []string{"BuildGoFiles", "BuildGoPackage"}

	for _, testName := range testNames {
		t.Run(testName, func(t *testing.T) {
			module, symbols := buildLoadable(t, conf, testName, data)
			newThingFunc := symbols["NewThing"].(func() common.SomeInterface)

			thing := newThingFunc()
			err := checkStackTrace(t, thing)
			if err != nil {
				t.Fatal(err)
			}

			err = module.Unload()
			if err != nil {
				t.Fatal(err)
			}
			if err != nil {
				t.Fatal(err)
			}
			err = module.UnloadStringMap()
		})
	}
}

func checkStackTrace(t *testing.T, thing common.SomeInterface) (err error) {
	defer func() {
		if v := recover(); v != nil {
			stack := debug.Stack()
			indices := make([]int, 9)
			orderedBytes := [][]byte{
				[]byte("/test.go:15"),
				[]byte("/file1.go:7"),
				[]byte(".(*SomeType).callSite1("),
				[]byte("/file2.go:11"),
				[]byte(".(*SomeType).callSite2("),
				[]byte("/file3.go:13"),
				[]byte(".(*SomeType).callSite3("),
				[]byte("/file4.go:16"),
				[]byte(".(*SomeType).callSite4("),
			}
			indices[0] = bytes.LastIndex(stack, orderedBytes[8])
			indices[1] = bytes.LastIndex(stack, orderedBytes[7])
			indices[2] = bytes.LastIndex(stack, orderedBytes[6])
			indices[3] = bytes.LastIndex(stack, orderedBytes[5])
			indices[4] = bytes.LastIndex(stack, orderedBytes[4])
			indices[5] = bytes.LastIndex(stack, orderedBytes[3])
			indices[6] = bytes.LastIndex(stack, orderedBytes[2])
			indices[7] = bytes.LastIndex(stack, orderedBytes[1])
			indices[8] = bytes.LastIndex(stack, orderedBytes[0])
			for i, index := range indices {
				if index == -1 {
					err = fmt.Errorf("expected stack trace to contain %s, but wasn't found, got \n%s", orderedBytes[8-i], stack)
					return
				}
			}
			if !sort.IsSorted(sort.IntSlice(indices)) {
				err = fmt.Errorf("expected stack trace to be ordered like %s, but got \n %s", orderedBytes, stack)
			}
		}
	}()
	_, err = thing.Method1(common.SomeStruct{Val1: "FECK"})
	if err != nil {
		t.Fatal(err)
	}
	return nil
}

func TestJitGoroutines(t *testing.T) {
	conf := jit.BuildConfig{
		GoBinary:            goBinary,
		KeepTempFiles:       false,
		ExtraBuildFlags:     nil,
		BuildEnv:            nil,
		TmpDir:              "",
		DebugLog:            false,
		HeapStrings:         heapStrings,
		StringContainerSize: stringContainerSize,
	}

	data := testData{
		files: []string{"./testdata/test_goroutines/test.go"},
		pkg:   "testdata/test_goroutines",
	}
	testNames := []string{"BuildGoFiles", "BuildGoPackage", "BuildGoText"}

	for _, testName := range testNames {
		t.Run(testName, func(t *testing.T) {
			module, symbols := buildLoadable(t, conf, testName, data)
			newThing := symbols["NewThing"].(func() common.StartStoppable)
			thing := newThing()
			before := runtime.NumGoroutine()
			err := thing.Start()
			if err != nil {
				t.Fatal(err)
			}
			afterStart := runtime.NumGoroutine()
			thing.InChan() <- common.SomeStruct{Val1: "not working"}
			output := <-thing.OutChan()

			if output.Val1.(string) != "Goroutine working" {
				t.Fatalf("expected 'Goroutine working', got %s", output.Val1)
			}

			err = thing.Stop()
			afterStop := runtime.NumGoroutine()
			if before != afterStop {
				t.Fatalf("expected num goroutines %d and %d to be equal", before, afterStop)
			}
			if afterStart != before+1 {
				t.Fatalf("expected afterStart to be 1 greater than before, got %d and %d", afterStart, before)
			}
			err = module.Unload()
			if err != nil {
				t.Fatal(err)
			}
			if err != nil {
				t.Fatal(err)
			}
			err = module.UnloadStringMap()
		})
	}
}

func TestLoadUnloadMultipleModules(t *testing.T) {
	conf := jit.BuildConfig{
		GoBinary:            goBinary,
		KeepTempFiles:       false,
		ExtraBuildFlags:     nil,
		BuildEnv:            nil,
		TmpDir:              "",
		DebugLog:            false,
		HeapStrings:         heapStrings,
		StringContainerSize: stringContainerSize,
	}

	data1 := testData{
		files: []string{"./testdata/test_simple_func/test.go"},
		pkg:   "testdata/test_simple_func",
	}
	data2 := testData{
		files: []string{"./testdata/test_goroutines/test.go"},
		pkg:   "testdata/test_goroutines",
	}
	testNames := []string{"BuildGoFiles", "BuildGoPackage", "BuildGoText"}
	for _, testName := range testNames {
		t.Run(testName, func(t *testing.T) {
			module1, symbols1 := buildLoadable(t, conf, testName, data1)
			module2, symbols2 := buildLoadable(t, conf, testName, data2)

			addFunc := symbols1["Add"].(func(a, b int) int)
			result := addFunc(5, 6)
			if result != 11 {
				t.Errorf("expected %d, got %d", 11, result)
			}

			handleBytesFunc := symbols1["HandleBytes"].(func(input interface{}) ([]byte, error))
			bytesOut, err := handleBytesFunc([]byte{1, 2, 3})
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(bytesOut, []byte{1, 2, 3}) {
				t.Errorf("expected %v, got %v", []byte{1, 2, 3}, bytesOut)
			}

			newThing := symbols2["NewThing"].(func() common.StartStoppable)
			thing := newThing()
			before := runtime.NumGoroutine()
			err = thing.Start()
			if err != nil {
				t.Fatal(err)
			}
			afterStart := runtime.NumGoroutine()
			thing.InChan() <- common.SomeStruct{Val1: "not working"}
			output := <-thing.OutChan()

			if output.Val1.(string) != "Goroutine working" {
				t.Fatalf("expected 'Goroutine working', got %s", output.Val1)
			}

			err = thing.Stop()
			afterStop := runtime.NumGoroutine()
			if before != afterStop {
				t.Fatalf("expected num goroutines %d and %d to be equal", before, afterStop)
			}
			if afterStart != before+1 {
				t.Fatalf("expected afterStart to be 1 greater than before, got %d and %d", afterStart, before)
			}

			// Don't unload in reverse order
			err = module1.Unload()
			if err != nil {
				t.Fatal(err)
			}
			err = module1.UnloadStringMap()
			if err != nil {
				t.Fatal(err)
			}
			err = module2.Unload()
			if err != nil {
				t.Fatal(err)
			}
			err = module2.UnloadStringMap()
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestStackSplit(t *testing.T) {
	conf := jit.BuildConfig{
		GoBinary:            goBinary,
		KeepTempFiles:       false,
		ExtraBuildFlags:     nil,
		BuildEnv:            nil,
		TmpDir:              "",
		DebugLog:            false,
		HeapStrings:         heapStrings,
		StringContainerSize: stringContainerSize,
	}

	data := testData{
		files: []string{"./testdata/test_stack_split/test.go"},
		pkg:   "testdata/test_stack_split",
	}
	testNames := []string{"BuildGoFiles", "BuildGoPackage", "BuildGoText"}

	for _, testName := range testNames {
		t.Run(testName, func(t *testing.T) {
			module, symbols := buildLoadable(t, conf, testName, data)
			RecurseUntilMaxDepth := symbols["RecurseUntilMaxDepth"].(func(depth int, oldAddr, prevDiff uintptr, splitCount int) int)

			var someVarOnStack int
			addr := uintptr(unsafe.Pointer(&someVarOnStack))

			splitCount := RecurseUntilMaxDepth(0, addr, 144, 0)

			if splitCount < 12 {
				t.Errorf("expected at least 12 stack splits")
			}
			fmt.Println("Split count:", splitCount)
			err := module.Unload()
			if err != nil {
				t.Fatal(err)
			}
			if err != nil {
				t.Fatal(err)
			}
			err = module.UnloadStringMap()
		})
	}
}

func TestSimpleAsmFuncs(t *testing.T) {
	conf := jit.BuildConfig{
		GoBinary:            goBinary,
		KeepTempFiles:       false,
		ExtraBuildFlags:     nil,
		BuildEnv:            nil,
		TmpDir:              "",
		DebugLog:            false,
		HeapStrings:         heapStrings,
		StringContainerSize: stringContainerSize,
	}

	data := testData{
		pkg: "testdata/test_simple_asm_func",
	}
	testNames := []string{"BuildGoPackage"}

	for _, testName := range testNames {
		t.Run(testName, func(t *testing.T) {
			module, symbols := buildLoadable(t, conf, testName, data)

			myMax := symbols["MyMax"].(func(a, b float64) float64)
			allMaxes := symbols["AllTheMaxes"].(func(a, b float64) (float64, float64, float64, float64))

			myMaxResult := myMax(5, 999)
			a, b, c, d := allMaxes(5, 999)
			if myMaxResult != 999 {
				t.Fatalf("expected myMaxResult to be 999, got %f", myMaxResult)
			}
			if a != 999 {
				t.Fatalf("expected a to be 999, got %f", a)
			}
			if b != 999 {
				t.Fatalf("expected b to be 999, got %f", b)
			}
			if c != 999 {
				t.Fatalf("expected c to be 999, got %f", c)
			}
			if d != 32 {
				t.Fatalf("expected d to be 32, got %f", d)
			}
			err := module.Unload()
			if err != nil {
				t.Fatal(err)
			}
			if err != nil {
				t.Fatal(err)
			}
			err = module.UnloadStringMap()
		})
	}
}

func TestComplexAsmFuncs(t *testing.T) {
	backupGoMod, err := os.ReadFile("go.mod")
	if err != nil {
		t.Fatal(err)
	}
	backupGoSum, err := os.ReadFile("go.sum")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		err = os.WriteFile("go.mod", backupGoMod, os.ModePerm)
		if err != nil {
			t.Error(err)
		}
		err = os.WriteFile("go.sum", backupGoSum, os.ModePerm)
		if err != nil {
			t.Error(err)
		}
	}()

	conf := jit.BuildConfig{
		GoBinary:            goBinary,
		KeepTempFiles:       false,
		ExtraBuildFlags:     nil,
		BuildEnv:            nil,
		TmpDir:              "./",
		DebugLog:            false,
		HeapStrings:         heapStrings,
		StringContainerSize: stringContainerSize,
	}

	data := testData{
		files: []string{"./testdata/test_complex_asm_func/test.go"},
		pkg:   "testdata/test_complex_asm_func",
	}
	testNames := []string{"BuildGoFiles", "BuildGoPackage", "BuildGoText"}

	for _, testName := range testNames {
		t.Run(testName, func(t *testing.T) {
			module, symbols := buildLoadable(t, conf, testName, data)

			matPow := symbols["MatPow"].(func())

			matPow()
			matPow()
			err := module.Unload()
			if err != nil {
				t.Fatal(err)
			}
			if err != nil {
				t.Fatal(err)
			}
			err = module.UnloadStringMap()
		})
	}
}

// https://github.com/pkujhd/goloader/issues/55
func TestIssue55(t *testing.T) {
	conf := jit.BuildConfig{
		KeepTempFiles:   false,
		ExtraBuildFlags: nil,
		BuildEnv:        nil,
		TmpDir:          "",
		DebugLog:        true,
	}

	data := testData{
		files: []string{"./testdata/test_issue55/t/t.go"},
		pkg:   "./testdata/test_issue55/t",
	}

	testNames := []string{"BuildGoFiles", "BuildGoPackage", "BuildGoText"}

	for _, testName := range testNames {
		t.Run(testName, func(t *testing.T) {
			module, symbols := buildLoadable(t, conf, testName, data)

			test := symbols["Test"].(func(intf p.Intf) p.Intf)
			test(&p.Stru{})
			err := module.Unload()
			if err != nil {
				t.Fatal(err)
			}
			if err != nil {
				t.Fatal(err)
			}
			err = module.UnloadStringMap()
		})
	}
}

func TestPackageNameNotEqualToImportPath(t *testing.T) {
	backupGoMod, err := os.ReadFile("go.mod")
	if err != nil {
		t.Fatal(err)
	}
	backupGoSum, err := os.ReadFile("go.sum")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		err = os.WriteFile("go.mod", backupGoMod, os.ModePerm)
		if err != nil {
			t.Error(err)
		}
		err = os.WriteFile("go.sum", backupGoSum, os.ModePerm)
		if err != nil {
			t.Error(err)
		}
	}()

	conf := jit.BuildConfig{
		GoBinary:            goBinary,
		KeepTempFiles:       false,
		ExtraBuildFlags:     nil,
		BuildEnv:            nil,
		TmpDir:              "",
		DebugLog:            false,
		HeapStrings:         heapStrings,
		StringContainerSize: stringContainerSize,
	}

	data := testData{
		files: []string{"./testdata/test_package_path_not_import_path/test.go"},
		pkg:   "./testdata/test_package_path_not_import_path",
	}
	testNames := []string{"BuildGoFiles", "BuildGoPackage", "BuildGoText"}

	for _, testName := range testNames {
		t.Run(testName, func(t *testing.T) {
			module, symbols := buildLoadable(t, conf, testName, data)

			whatever := symbols["Whatever"].(func())

			whatever()
			err := module.Unload()
			if err != nil {
				t.Fatal(err)
			}
			if err != nil {
				t.Fatal(err)
			}
			err = module.UnloadStringMap()
		})
	}
}

func TestConvertOldAndNewTypes(t *testing.T) {
	conf := jit.BuildConfig{
		GoBinary:            goBinary,
		KeepTempFiles:       false,
		ExtraBuildFlags:     nil,
		BuildEnv:            nil,
		TmpDir:              "",
		DebugLog:            false,
		HeapStrings:         heapStrings,
		StringContainerSize: stringContainerSize,
	}

	data := testData{
		files: []string{"./testdata/test_conversion/test.go"},
		pkg:   "testdata/test_conversion",
	}
	testNames := []string{"BuildGoFiles", "BuildGoPackage", "BuildGoText"}
	for _, testName := range testNames {
		t.Run(testName, func(t *testing.T) {
			module1, symbols1 := buildLoadable(t, conf, testName, data)
			module2, symbols2 := buildLoadable(t, conf, testName, data)

			newThingFunc1 := symbols1["NewThingOriginal"].(func() common.SomeInterface)
			newThingFunc2 := symbols2["NewThingOriginal"].(func() common.SomeInterface)
			newThingIfaceFunc1 := symbols1["NewThingWithInterface"].(func() common.SomeInterface)
			newThingIfaceFunc2 := symbols2["NewThingWithInterface"].(func() common.SomeInterface)

			thing1 := newThingFunc1()
			thing2 := newThingFunc2()
			thingIface1 := newThingIfaceFunc1()
			thingIface2 := newThingIfaceFunc2()

			input := int64(123)
			out1, _ := thing1.Method1(common.SomeStruct{Val1: input, Val2: map[string]interface{}{}})
			current := out1.Val2["current"].(int64)
			if current != input {
				t.Fatalf("expected current to be the same as input: %d  %d", current, input)
			}

			newThing2, err := goloader.ConvertTypesAcrossModules(module1, module2, thing1, thing2)
			if err != nil {
				t.Fatal(err)
			}
			thing2 = newThing2.(common.SomeInterface)

			ifaceOut1, _ := thingIface1.Method1(common.SomeStruct{Val1: input, Val2: map[string]interface{}{}})
			ifaceCounter1 := ifaceOut1.Val2["exclusive_interface_counter"].(string)
			byteReader1 := ifaceOut1.Val2["bytes_reader_output"].([]byte)
			ifaceCurrent1 := ifaceOut1.Val2["current"].(int64)

			ifaceOut12, _ := thingIface1.Method1(common.SomeStruct{Val1: []byte{4, 5, 6}, Val2: map[string]interface{}{}})
			ifaceCounter12 := ifaceOut12.Val2["exclusive_interface_counter"].(string)
			byteReader12 := ifaceOut12.Val2["bytes_reader_output"].([]byte)
			ifaceCurrent12 := ifaceOut12.Val2["current"].(int64)
			_ = thingIface1.Method2(nil)

			newThingIface2, err := goloader.ConvertTypesAcrossModules(module1, module2, thingIface1, thingIface2)
			if err != nil {
				t.Fatal(err)
			}
			fmt.Println(thingIface1)
			fmt.Println(newThingIface2)

			thingIface2 = newThingIface2.(common.SomeInterface)

			// Unload thing1's types + methods entirely
			err = module1.Unload()

			if err != nil {
				t.Fatal(err)
			}
			err = module1.UnloadStringMap()

			ifaceOut2, _ := thingIface2.Method1(common.SomeStruct{Val1: 789, Val2: map[string]interface{}{}})

			ifaceCounter2 := ifaceOut2.Val2["exclusive_interface_counter"].(string)
			byteReader2 := ifaceOut2.Val2["bytes_reader_output"].([]byte)
			ifaceCurrent2 := ifaceOut2.Val2["current"].(int64)

			if ifaceCounter1 != "Counter: 124" {
				t.Fatalf("expected ifaceCounter1 to be 'Counter: 124', got %s", ifaceCounter1)
			}
			if ifaceCounter12 != "Counter: 125" {
				t.Fatalf("expected ifaceCounter12 to be 'Counter: 125', got %s", ifaceCounter12)
			}
			if ifaceCounter2 != "Counter: 126" {
				t.Fatalf("expected ifaceCounter2 to be 'Counter: 126', got %s", ifaceCounter2)
			}
			if !bytes.Equal(byteReader1, []byte{1, 2, 3}) {
				t.Fatalf("expected byteReader1 to be []byte{1,2,3}, got %v", byteReader1)
			}
			if !bytes.Equal(byteReader12, []byte{4, 5, 6}) {
				t.Fatalf("expected byteReader12 to be []byte{4,5,6}, got %v", byteReader12)
			}
			if !bytes.Equal(byteReader12, []byte{4, 5, 6}) {
				t.Fatalf("expected byteReader2 to be []byte{4,5,6}, got %v", byteReader2)
			}
			if !bytes.Equal(byteReader12, []byte{4, 5, 6}) {
				t.Fatalf("expected byteReader2 to be []byte{4,5,6}, got %v", byteReader2)
			}
			out2, _ := thing2.Method1(common.SomeStruct{Val1: nil, Val2: map[string]interface{}{}})
			converted2 := out2.Val2["current"].(int64)
			if converted2 != input {
				t.Fatalf("expected converted to be the same as input: %d  %d", converted2, input)
			}
			_ = thingIface2.Method2(nil)

			fmt.Println(ifaceCurrent1, ifaceCurrent12, ifaceCurrent2)
			if err != nil {
				t.Fatal(err)
			}

			err = module2.Unload()
			if err != nil {
				t.Fatal(err)
			}
			err = module2.UnloadStringMap()
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

// This test works and demonstrates the preservation of state across modules, but to avoid affecting what
// parts of net/http gets baked into the test binary for the earlier TestJitHttpGet, it is commented out.

/**

type SwapperMiddleware struct {
	handler http.Handler
	mutex   sync.RWMutex
}

func (h *SwapperMiddleware) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mutex.RLock()
	defer h.mutex.RUnlock()
	h.handler.ServeHTTP(w, r)
}

func TestStatefulHttpServer(t *testing.T) {
	conf := jit.BuildConfig{
		KeepTempFiles:   false,
		ExtraBuildFlags: nil,
		BuildEnv:        nil,
		TmpDir:          "",
		DebugLog:            false,
		HeapStrings:         heapStrings,
		StringContainerSize: stringContainerSize,
	}

	data := testData{
		files: []string{"./testdata/test_stateful_server/test.go"},
		pkg:   "./testdata/test_stateful_server",
	}

	testNames := []string{"BuildGoFiles", "BuildGoPackage", "BuildGoText"}

	for _, testName := range testNames {
		t.Run(testName, func(t *testing.T) {
			module, symbols := buildLoadable(t, conf, testName, data)

			makeHandler := symbols["MakeServer"].(func() http.Handler)
			handler := makeHandler()
			server := &http.Server{
				Addr: "localhost:9091",
			}
			h := &SwapperMiddleware{handler: handler}

			server.Handler = h
			go func() {
				_ = server.ListenAndServe()
			}()
			time.Sleep(time.Millisecond * 100)
			resp, err := http.Post("http://localhost:9091", "text/plain", strings.NewReader("test1"))
			if err != nil {
				t.Fatal(err)
			}
			body, _ := io.ReadAll(resp.Body)
			fmt.Println(string(body))

			module2, symbols2 := buildLoadable(t, conf, testName, data)

			makeHandler2 := symbols2["MakeServer"].(func() http.Handler)
			handler2 := makeHandler2()

			newHandler, err := goloader.ConvertTypesAcrossModules(module, module2, handler, handler2)
			if err != nil {
				t.Fatal(err)
			}

			h.mutex.Lock()
			h.handler = newHandler.(http.Handler)
			h.mutex.Unlock()

			err = module.Unload()
			if err != nil {
				t.Fatal(err)
			}

			resp, err = http.Post("http://localhost:9091", "text/plain", strings.NewReader("test2"))
			if err != nil {
				t.Fatal(err)
			}
			body, _ = io.ReadAll(resp.Body)
			fmt.Println(string(body))

			err = module2.Unload()
			if err != nil {
				t.Fatal(err)
			}

			server.Close()
		})
	}
}

*/

func TestCloneConnection(t *testing.T) {
	conf := jit.BuildConfig{
		GoBinary:            goBinary,
		KeepTempFiles:       false,
		ExtraBuildFlags:     nil,
		BuildEnv:            nil,
		TmpDir:              "./",
		DebugLog:            false,
		HeapStrings:         heapStrings,
		StringContainerSize: stringContainerSize,
	}

	data := testData{
		files: []string{"./testdata/test_clone_connection/test.go"},
		pkg:   "testdata/test_clone_connection",
	}
	testNames := []string{"BuildGoFiles", "BuildGoPackage", "BuildGoText"}

	listener, err := net.Listen("tcp", ":9091")
	if err != nil {
		t.Fatal(err)
	}
	keepAccepting := true
	var results [][]string
	go func() {
		connectionCount := 0
		for keepAccepting {
			conn, err := listener.Accept()
			connectionCount++
			var result []string
			results = append(results, result)
			if err != nil {
				if keepAccepting {
					t.Error("expected to continue accepting", err)
				}
				return
			}
			go func(c net.Conn, index int) {
				buf := make([]byte, 8)
				for {
					n, err := c.Read(buf)
					if err != nil {
						return
					}
					results[index-1] = append(results[index-1], string(buf[:n]))
				}
			}(conn, connectionCount)
		}
	}()

	for _, testName := range testNames {
		t.Run(testName, func(t *testing.T) {
			module1, symbols1 := buildLoadable(t, conf, testName, data)
			module2, symbols2 := buildLoadable(t, conf, testName, data)

			newDialerFunc1 := symbols1["NewConnDialer"].(func() common.MessageWriter)
			newDialerFunc2 := symbols2["NewConnDialer"].(func() common.MessageWriter)

			dialer1 := newDialerFunc1()
			dialer2 := newDialerFunc2()

			err = dialer1.Dial("localhost:9091")

			if err != nil {
				t.Fatal(err)
			}
			_, err = dialer1.WriteMessage("test1234")
			if err != nil {
				t.Fatal(err)
			}

			newDialer2, err := goloader.ConvertTypesAcrossModules(module1, module2, dialer1, dialer2)
			if err != nil {
				t.Fatal(err)
			}
			err = module1.Unload()
			if err != nil {
				t.Fatal(err)
			}
			err = module1.UnloadStringMap()
			if err != nil {
				t.Fatal(err)
			}
			dialer2 = newDialer2.(common.MessageWriter)
			_, err = dialer2.WriteMessage("test5678")
			if err != nil {
				t.Fatal(err)
			}
			err = dialer2.Close()

			err = module2.Unload()
			if err != nil {
				t.Fatal(err)
			}
			err = module2.UnloadStringMap()
			if err != nil {
				t.Fatal(err)
			}
		})
	}
	if len(results) != len(testNames) {
		t.Errorf("expected %d connection test results, got %d", len(testNames), len(results))
	}
	for _, result := range results {
		if len(result) != 2 {
			t.Errorf("expected 2 writes per connection, got %d", len(result))
		} else {
			if result[0] != "test1234" {
				t.Errorf("expected first write to be test1234, got %s", result[0])
			}
			if result[1] != "test5678" {
				t.Errorf("expected second write to be test5678, got %s", result[1])
			}
		}
	}
	keepAccepting = false
	_ = listener.Close()
}
