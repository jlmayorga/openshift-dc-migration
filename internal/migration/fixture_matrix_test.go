package migration

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestVersionFixtureMatrix(t *testing.T) {
	paths, err := filepath.Glob("testdata/ocp-*-dc.*")
	if err != nil || len(paths) != 9 {
		t.Fatalf("fixture matrix: %v %v", paths, err)
	}
	for _, path := range paths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			t.Parallel()
			objects, err := readDCFile(path)
			if err != nil {
				t.Fatal(err)
			}
			for _, d := range objects {
				before := d.DeepCopy()
				c, err := NewRunner(DefaultOptions()).Convert(d)
				if err != nil || len(c.Unsupported) > 0 {
					t.Fatalf("%v %v", err, c.Unsupported)
				}
				if !reflect.DeepEqual(before, d) {
					t.Fatal("source mutation")
				}
				if c.Deployment.Spec.Template.Labels["deploymentconfig"] != "web" {
					t.Fatal("lost Service selector")
				}
			}
		})
	}
}
