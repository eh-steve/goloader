//go:build darwin
// +build darwin

package mmap

// https://developer.apple.com/library/archive/documentation/Performance/Conceptual/ManagingMemory/Articles/VMPages.html
func getCurrentProcMaps() ([]Mapping, error) {
	pid := os.Getpid()
	cmd := exec.Command("vmmap", "-v", fmt.Sprintf("%d", pid))
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("could not run 'vmmap -v %d': %w", pid, err)
	}

	// TODO - parse output properly
	fmt.Printf("%s", output)
	return nil, nil
}
