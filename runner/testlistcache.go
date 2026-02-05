package runner

import (
	"strings"
	"time"

	"github.com/runar-rkmedia/donotnet/cache"
	"github.com/runar-rkmedia/donotnet/project"
)

// testListCacheImpl implements coverage.TestListCache using cache.DB.
type testListCacheImpl struct {
	db           *cache.DB
	gitRoot      string
	forwardGraph map[string][]string
	argsHash     string
}

func newTestListCache(db *cache.DB, gitRoot string, forwardGraph map[string][]string) *testListCacheImpl {
	return &testListCacheImpl{
		db:           db,
		gitRoot:      gitRoot,
		forwardGraph: forwardGraph,
		argsHash:     HashArgs([]string{"list-tests"}),
	}
}

func (c *testListCacheImpl) LookupTestList(p *project.Project) []string {
	key := ProjectCacheKey(p, c.gitRoot, c.forwardGraph, c.argsHash)

	result := c.db.Lookup(key)
	if result == nil || len(result.Output) == 0 {
		return nil
	}

	tests := strings.Split(strings.TrimSpace(string(result.Output)), "\n")
	if len(tests) == 0 || tests[0] == "" {
		return nil
	}
	return tests
}

func (c *testListCacheImpl) StoreTestList(p *project.Project, tests []string) {
	key := ProjectCacheKey(p, c.gitRoot, c.forwardGraph, c.argsHash)

	output := strings.Join(tests, "\n")
	c.db.Mark(key, time.Now(), true, []byte(output), "list-tests")
}
