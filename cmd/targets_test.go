package cmd

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/spf13/cobra"
)

func TestSplitArgsAtDash(t *testing.T) {
	tests := []struct {
		name         string
		args         []string // args to pass to the command
		wantPaths    []string
		wantDotnet   []string
	}{
		{
			name:       "no args",
			args:       nil,
			wantPaths:  []string{}, // cobra passes empty slice, not nil
			wantDotnet: nil,
		},
		{
			name:       "only passthrough (-- first)",
			args:       []string{"--", "--verbosity", "quiet"},
			wantPaths:  []string{},
			wantDotnet: []string{"--verbosity", "quiet"},
		},
		{
			name:       "only positional (no --)",
			args:       []string{"path/foo.csproj"},
			wantPaths:  []string{"path/foo.csproj"},
			wantDotnet: nil,
		},
		{
			name:       "positional and passthrough",
			args:       []string{"path/foo.csproj", "--", "--verbosity", "quiet"},
			wantPaths:  []string{"path/foo.csproj"},
			wantDotnet: []string{"--verbosity", "quiet"},
		},
		{
			name:       "multiple positional and passthrough",
			args:       []string{"a.csproj", "b.csproj", "--", "--no-build"},
			wantPaths:  []string{"a.csproj", "b.csproj"},
			wantDotnet: []string{"--no-build"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotPaths, gotDotnet []string
			cmd := &cobra.Command{
				Use: "test",
				RunE: func(cmd *cobra.Command, args []string) error {
					gotPaths, gotDotnet = splitArgsAtDash(cmd, args)
					return nil
				},
			}
			cmd.Flags().Bool("force", false, "") // dummy flag

			root := &cobra.Command{Use: "root"}
			root.AddCommand(cmd)
			root.SetArgs(append([]string{"test"}, tt.args...))
			root.Execute()

			if !reflect.DeepEqual(gotPaths, tt.wantPaths) {
				t.Errorf("paths = %v, want %v", gotPaths, tt.wantPaths)
			}
			if !reflect.DeepEqual(gotDotnet, tt.wantDotnet) {
				t.Errorf("dotnet = %v, want %v", gotDotnet, tt.wantDotnet)
			}
		})
	}
}

func TestResolveTargets_Empty(t *testing.T) {
	result, err := resolveTargets(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Fatalf("expected nil, got %v", result)
	}
}

func TestResolveTargets_Csproj(t *testing.T) {
	dir := t.TempDir()
	csproj := filepath.Join(dir, "Foo.csproj")
	os.WriteFile(csproj, []byte("<Project/>"), 0644)

	result, err := resolveTargets([]string{csproj})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 result, got %d", len(result))
	}
	if result[0] != csproj {
		t.Errorf("expected %q, got %q", csproj, result[0])
	}
}

func TestResolveTargets_Sln(t *testing.T) {
	dir := t.TempDir()
	sln := filepath.Join(dir, "MySolution.sln")
	os.WriteFile(sln, []byte(""), 0644)

	result, err := resolveTargets([]string{sln})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 result, got %d", len(result))
	}
}

func TestResolveTargets_Directory(t *testing.T) {
	dir := t.TempDir()
	subdir := filepath.Join(dir, "src")
	os.MkdirAll(subdir, 0755)

	result, err := resolveTargets([]string{subdir})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 result, got %d", len(result))
	}
}

func TestResolveTargets_Nonexistent(t *testing.T) {
	_, err := resolveTargets([]string{"/nonexistent/path/Foo.csproj"})
	if err == nil {
		t.Fatal("expected error for nonexistent path")
	}
}

func TestResolveTargets_InvalidExtension(t *testing.T) {
	dir := t.TempDir()
	txt := filepath.Join(dir, "readme.txt")
	os.WriteFile(txt, []byte("hello"), 0644)

	_, err := resolveTargets([]string{txt})
	if err == nil {
		t.Fatal("expected error for non-.csproj/.sln file")
	}
}

func TestResolveTargets_Multiple(t *testing.T) {
	dir := t.TempDir()
	csproj1 := filepath.Join(dir, "A.csproj")
	csproj2 := filepath.Join(dir, "B.csproj")
	os.WriteFile(csproj1, []byte("<Project/>"), 0644)
	os.WriteFile(csproj2, []byte("<Project/>"), 0644)

	result, err := resolveTargets([]string{csproj1, csproj2})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 2 {
		t.Fatalf("expected 2 results, got %d", len(result))
	}
}

func TestInjectMappedFlags(t *testing.T) {
	tests := []struct {
		name          string
		dotnetArgs    []string
		filter        string
		configuration string
		want          []string
	}{
		{
			name:       "no flags",
			dotnetArgs: []string{"--no-build"},
			want:       []string{"--no-build"},
		},
		{
			name:       "filter only",
			dotnetArgs: []string{"--no-build"},
			filter:     "Name~Foo",
			want:       []string{"--filter", "Name~Foo", "--no-build"},
		},
		{
			name:          "configuration only",
			dotnetArgs:    []string{"--no-build"},
			configuration: "Release",
			want:          []string{"-c", "Release", "--no-build"},
		},
		{
			name:          "both flags",
			dotnetArgs:    nil,
			filter:        "Name~Foo",
			configuration: "Release",
			want:          []string{"--filter", "Name~Foo", "-c", "Release"},
		},
		{
			name:       "nil dotnetArgs with filter",
			dotnetArgs: nil,
			filter:     "Name~Bar",
			want:       []string{"--filter", "Name~Bar"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := injectMappedFlags(tt.dotnetArgs, tt.filter, tt.configuration)
			if len(got) != len(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("index %d: got %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestCheckFilterConflict(t *testing.T) {
	tests := []struct {
		name         string
		nativeFilter string
		dotnetArgs   []string
		wantErr      bool
	}{
		{
			name:         "no conflict - native only",
			nativeFilter: "Name~Foo",
			dotnetArgs:   []string{"--no-build"},
			wantErr:      false,
		},
		{
			name:         "no conflict - passthrough only",
			nativeFilter: "",
			dotnetArgs:   []string{"--filter", "Name~Foo"},
			wantErr:      false,
		},
		{
			name:         "no conflict - neither",
			nativeFilter: "",
			dotnetArgs:   nil,
			wantErr:      false,
		},
		{
			name:         "conflict - both specified",
			nativeFilter: "Name~Foo",
			dotnetArgs:   []string{"--filter", "Name~Bar"},
			wantErr:      true,
		},
		{
			name:         "conflict - passthrough with equals",
			nativeFilter: "Name~Foo",
			dotnetArgs:   []string{"--filter=Name~Bar"},
			wantErr:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkFilterConflict(tt.nativeFilter, tt.dotnetArgs)
			if tt.wantErr && err == nil {
				t.Error("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestIsPathArg(t *testing.T) {
	dir := t.TempDir()
	subdir := filepath.Join(dir, "src")
	os.MkdirAll(subdir, 0755)

	tests := []struct {
		name string
		arg  string
		want bool
	}{
		{"csproj extension", "Foo.csproj", true},
		{"sln extension", "My.sln", true},
		{"existing directory", subdir, true},
		{"random flag", "--verbose", false},
		{"random string", "hello", false},
		{"nonexistent directory", "/nonexistent/path", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isPathArg(tt.arg)
			if got != tt.want {
				t.Errorf("isPathArg(%q) = %v, want %v", tt.arg, got, tt.want)
			}
		})
	}
}
