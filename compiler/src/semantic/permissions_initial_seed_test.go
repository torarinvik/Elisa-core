//go:build cgo

package semantic

import (
	"reflect"
	"testing"
)

func TestPermissionInitialPassSchedulesOnlyOutdatedReaders(t *testing.T) {
	for _, limit := range []int{permissionInitialReadLimit, 0} {
		f := newPermissionGraphFixture(256)
		f.edge(0, 1)
		f.edge(1, -1)
		oracle := newPermissionGraphFixture(256)
		oracle.edge(0, 1)
		oracle.edge(1, -1)
		result := f.a.solveFunctionPermissionEffectsWithReadLimit(f.decls, len(f.decls)+4, limit)
		permissionReferenceSweep(oracle.a, oracle.decls)
		if !result.Converged || !reflect.DeepEqual(permissionFixtureRows(f), permissionFixtureRows(oracle)) {
			t.Fatalf("incorrect fixed point with limit%d: %+v", limit, result)
		}
		want := 257
		if limit == 0 {
			want = 512
		}
		if result.Evaluations != want {
			t.Fatalf("limit%d evaluated%d, want%d", limit, result.Evaluations, want)
		}
		if f.a.permissionInference != nil {
			t.Fatal("observer leaked")
		}
	}
}

func TestPermissionInitialPassFreshLaterReadersNeedNoConfirmation(t *testing.T) {
	f := newPermissionGraphFixture(16)
	f.edge(0, -1)
	for i := 1; i < len(f.types); i++ {
		f.edge(i, i-1)
	}
	result := f.a.solveFunctionPermissionEffects(f.decls, 1)
	if !result.Converged || result.Evaluations != 16 || result.Pending != 0 {
		t.Fatalf("fresh ordered readers rescanned: %+v", result)
	}
	oracle := newPermissionGraphFixture(16)
	oracle.edge(0, -1)
	for i := 1; i < len(oracle.types); i++ {
		oracle.edge(i, i-1)
	}
	permissionReferenceSweep(oracle.a, oracle.decls)
	if !reflect.DeepEqual(permissionFixtureRows(f), permissionFixtureRows(oracle)) {
		t.Fatal("ordered pass differs from reference fixed point")
	}
}

func TestPermissionInitialTraceFallbackMatchesNormalScheduler(t *testing.T) {
	for _, limit := range []int{0, 1, 4, 16} {
		f := newPermissionGraphFixture(24)
		oracle := newPermissionGraphFixture(24)
		for i := 0; i < 23; i++ {
			f.edge(i, i+1)
			oracle.edge(i, i+1)
		}
		f.edge(23, -1)
		oracle.edge(23, -1)
		result := f.a.solveFunctionPermissionEffectsWithReadLimit(f.decls, len(f.decls)+4, limit)
		permissionReferenceSweep(oracle.a, oracle.decls)
		if !result.Converged || !reflect.DeepEqual(permissionFixtureRows(f), permissionFixtureRows(oracle)) {
			t.Fatalf("fallback%d changed rows: %+v", limit, result)
		}
	}
}
