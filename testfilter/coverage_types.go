package testfilter

import (
	"encoding/json"
	"os"
	"time"
)

// TestCoverageMap maps source files to the tests that cover them
type TestCoverageMap struct {
	Project        string              `json:"project"`
	FileToTests    map[string][]string `json:"file_to_tests"`    // source file -> test names
	TestToFiles    map[string][]string `json:"test_to_files"`    // test name -> source files
	GeneratedAt    time.Time           `json:"generated_at"`
	TotalTests     int                 `json:"total_tests"`
	ProcessedTests int                 `json:"processed_tests"`
}

// LoadTestCoverageMap loads an existing coverage map from disk
func LoadTestCoverageMap(path string) (*TestCoverageMap, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var m TestCoverageMap
	if err := json.NewDecoder(f).Decode(&m); err != nil {
		return nil, err
	}
	return &m, nil
}

// SaveTestCoverageMap saves a coverage map to disk
func SaveTestCoverageMap(path string, m *TestCoverageMap) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	return enc.Encode(m)
}
