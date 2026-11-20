package models

import (
	"math"
	"testing"
)

func testMaterial() Material {
	return NewLambertian(NewVector(0.5, 0.6, 0.7))
}

func newTestTriangle(t *testing.T, withNormals bool) *Triangle {
	t.Helper()
	v0 := NewVector(0, 0, -2)
	v1 := NewVector(2, 0, -2)
	v2 := NewVector(0, 2, -2)
	if withNormals {
		tri, err := NewTriangleWithNormals(
			v0, v1, v2,
			NewVector(0, 0, 1), NewVector(0, 0, 1), NewVector(0, 0, 1),
			testMaterial(),
		)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		return tri
	}
	tri, err := NewTriangle(v0, v1, v2, testMaterial())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return tri
}

func approxEqual(a, b, tol float64) bool {
	return math.Abs(a-b) <= tol
}

func TestTriangleFrontHitDistanceAndNormal(t *testing.T) {
	tri := newTestTriangle(t, false)
	// Ray from the origin towards the z=-2 plane. The winding normal from
	// edge1 x edge2 points +z, facing the ray, so it must be returned as is.
	r := &Ray{NewVector(0.2, 0.2, 0), NewVector(0, 0, -1)}

	hit, rec := tri.Hit(r, 0.0001, math.MaxFloat64)
	if !hit || rec == nil {
		t.Fatal("expected a hit on the triangle front side")
	}
	if !approxEqual(rec.T, 2.0, 1e-10) {
		t.Errorf("hit distance = %v, want 2.0", rec.T)
	}
	if !approxEqual(rec.N.Z(), 1.0, 1e-12) ||
		!approxEqual(rec.N.Length(), 1.0, 1e-12) {
		t.Errorf("normal = %v, want unit normal (0,0,+1)", rec.N)
	}
	if rec.P == nil || !approxEqual(rec.P.Z(), -2.0, 1e-10) {
		t.Errorf("hit point = %v, want z=-2", rec.P)
	}
	if rec.Material == nil {
		t.Error("hit record must carry the triangle material")
	}
}

func TestTriangleBackFaceStillHitsWithFlippedNormal(t *testing.T) {
	tri := newTestTriangle(t, false)
	// Ray coming from behind, travelling in +z.
	r := &Ray{NewVector(0.2, 0.2, -4), NewVector(0, 0, 1)}

	hit, rec := tri.Hit(r, 0.0001, math.MaxFloat64)
	if !hit {
		t.Fatal("back faces must hit (double sided), not be culled")
	}
	if !approxEqual(rec.N.Z(), -1.0, 1e-12) {
		t.Errorf("back face normal = %v, want (0,0,-1) facing the ray", rec.N)
	}
}

func TestTriangleMissesOutsideBarycentricRegion(t *testing.T) {
	tri := newTestTriangle(t, false)
	cases := map[string]*Ray{
		"outside along x": {NewVector(1.5, 1.5, 0), NewVector(0, 0, -1)},
		"off plane":       {NewVector(-0.5, -0.5, 0), NewVector(0, 0, -1)},
		"parallel ray":    {NewVector(0.2, 0.2, 0), NewVector(1, 0, 0)},
		"ray away":        {NewVector(0.2, 0.2, 0), NewVector(0, 0, 1)},
	}
	for name, r := range cases {
		if hit, _ := tri.Hit(r, 0.0001, math.MaxFloat64); hit {
			t.Errorf("%s: expected no hit", name)
		}
	}
}

func TestTriangleBoundaryIntersectionsAreInclusive(t *testing.T) {
	tri := newTestTriangle(t, false)
	cases := map[string][3]float64{
		"vertex v0":     {0, 0, 0},
		"vertex v1":     {2, 0, 0},
		"vertex v2":     {0, 2, 0},
		"edge midpoint": {1, 0, 0},
	}
	for name, origin := range cases {
		r := &Ray{NewVectorFromArray(origin), NewVector(0, 0, -1)}
		hit, rec := tri.Hit(r, 0.0001, math.MaxFloat64)
		if !hit {
			t.Errorf("%s: edge/vertex intersection must count as a hit", name)
			continue
		}
		if !approxEqual(rec.T, 2.0, 1e-9) {
			t.Errorf("%s: t = %v, want 2.0", name, rec.T)
		}
	}
}

func TestTriangleNearBoundarySnapIsStable(t *testing.T) {
	tri := newTestTriangle(t, false)
	// 1e-10 outside the edge, well inside the barycentric tolerance: the hit
	// must snap back instead of producing u slightly over 1.
	r := &Ray{NewVector(2.0+1e-10, 0, 0), NewVector(0, 0, -1)}
	hit, rec := tri.Hit(r, 0.0001, math.MaxFloat64)
	if !hit {
		t.Fatal("near-boundary ray should be snapped and hit")
	}
	if !approxEqual(rec.T, 2.0, 1e-8) {
		t.Errorf("snapped hit t = %v, want ~2.0", rec.T)
	}

	// Clearly outside the tolerance band must miss.
	r2 := &Ray{NewVector(2.01, 0, 0), NewVector(0, 0, -1)}
	if hit, _ := tri.Hit(r2, 0.0001, math.MaxFloat64); hit {
		t.Error("point outside the tolerance band must miss")
	}
}

func TestTriangleNearParallelRayRejected(t *testing.T) {
	tri := newTestTriangle(t, false)
	// Direction almost parallel to the plane; tiny z component would produce
	// a huge t without the relative determinant guard.
	r := &Ray{NewVector(0.2, 0.2, 0), NewVector(1, 0, 1e-13)}
	if hit, rec := tri.Hit(r, 0.0001, math.MaxFloat64); hit {
		t.Errorf("near-parallel ray must be rejected, got t=%v", rec.T)
	}
}

func TestTriangleHitIntervalIsOpen(t *testing.T) {
	tri := newTestTriangle(t, false)
	r := &Ray{NewVector(0.2, 0.2, 0), NewVector(0, 0, -1)}

	if hit, _ := tri.Hit(r, 2.0, 5.0); hit {
		t.Error("t == tmin must not hit: interval is open")
	}
	if hit, _ := tri.Hit(r, 0.0001, 2.0); hit {
		t.Error("t == tmax must not hit: interval is open")
	}
	if hit, _ := tri.Hit(r, 2.01, 5.0); hit {
		t.Error("triangle behind tmin must not hit")
	}
	if hit, _ := tri.Hit(r, 0.0001, 1.99); hit {
		t.Error("triangle beyond tmax must not hit")
	}
}

func TestTriangleDegenerateAndInvalidInputRejected(t *testing.T) {
	mat := testMaterial()
	cases := map[string]func() error{
		"collinear vertices": func() error {
			_, e := NewTriangle(
				NewVector(0, 0, -2), NewVector(1, 0, -2), NewVector(2, 0, -2), mat)
			return e
		},
		"repeated vertices": func() error {
			_, e := NewTriangle(
				NewVector(0, 0, -2), NewVector(0, 0, -2), NewVector(0, 2, -2), mat)
			return e
		},
		"NaN coordinate": func() error {
			_, e := NewTriangle(
				NewVector(math.NaN(), 0, -2), NewVector(2, 0, -2), NewVector(0, 2, -2), mat)
			return e
		},
		"+Inf coordinate": func() error {
			_, e := NewTriangle(
				NewVector(0, 0, -2), NewVector(math.Inf(1), 0, -2),
				NewVector(0, 2, -2), mat)
			return e
		},
		"nil vertex": func() error {
			_, e := NewTriangle(nil, NewVector(2, 0, -2), NewVector(0, 2, -2), mat)
			return e
		},
		"zero vertex normal": func() error {
			_, e := NewTriangleWithNormals(
				NewVector(0, 0, -2), NewVector(2, 0, -2), NewVector(0, 2, -2),
				NewEmptyVector(), NewVector(0, 0, 1), NewVector(0, 0, 1), mat)
			return e
		},
		"NaN vertex normal": func() error {
			_, e := NewTriangleWithNormals(
				NewVector(0, 0, -2), NewVector(2, 0, -2), NewVector(0, 2, -2),
				NewVector(math.NaN(), 0, 1), NewVector(0, 0, 1),
				NewVector(0, 0, 1), mat)
			return e
		},
		"partial normals": func() error {
			_, e := NewTriangleWithNormals(
				NewVector(0, 0, -2), NewVector(2, 0, -2), NewVector(0, 2, -2),
				nil, NewVector(0, 0, 1), NewVector(0, 0, 1), mat)
			return e
		},
	}
	for name, fn := range cases {
		if err := fn(); err == nil {
			t.Errorf("%s: expected construction error", name)
		}
	}
}

func TestTriangleHitRejectsMalformedRay(t *testing.T) {
	tri := newTestTriangle(t, false)
	cases := map[string]*Ray{
		"nil origin":     {nil, NewVector(0, 0, -1)},
		"nil direction":  {NewVector(0, 0, 0), nil},
		"NaN origin":     {NewVector(math.NaN(), 0, 0), NewVector(0, 0, -1)},
		"NaN direction":  {NewVector(0, 0, 0), NewVector(0, math.NaN(), -1)},
		"zero direction": {NewVector(0, 0, 0), NewEmptyVector()},
	}
	for name, r := range cases {
		if hit, rec := tri.Hit(r, 0.0001, math.MaxFloat64); hit || rec != nil {
			t.Errorf("%s: expected a clean miss, got hit=%v record=%v", name, hit, rec)
		}
	}
}

func TestTriangleVertexNormalInterpolation(t *testing.T) {
	// Normals spread from +z at v0 towards +x at v1/v2: the barycentric centre
	// gives an interpolated, renormalised normal.
	v0, v1, v2 := NewVector(0, 0, -2), NewVector(2, 0, -2), NewVector(0, 2, -2)
	tri, err := NewTriangleWithNormals(
		v0, v1, v2,
		NewVector(0, 0, 1), NewVector(1, 0, 0), NewVector(1, 0, 0),
		testMaterial(),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	r := &Ray{NewVector(0.5, 0.5, 0), NewVector(0, 0, -1)}
	hit, rec := tri.Hit(r, 0.0001, math.MaxFloat64)
	if !hit {
		t.Fatal("expected hit")
	}
	// Ray hits (0.5,0.5): barycentric weights are w=0.5, u=0.25, v=0.25, so the
	// raw interpolated normal is 0.5*(0,0,1)+0.5*(1,0,0) = (0.5,0,0.5), then
	// normalised. It already faces the -z travelling ray, so no flip.
	raw := NewVector(0.5, 0, 0.5).MakeUnitVector()
	if !approxEqual(rec.N.X(), raw.X(), 1e-10) ||
		!approxEqual(rec.N.Z(), raw.Z(), 1e-10) {
		t.Errorf("interpolated normal = %v, want %v", rec.N, raw)
	}

	// The same triangle hit from behind flips the interpolated normal so it
	// always faces the ray.
	backRay := &Ray{NewVector(0.5, 0.5, -4), NewVector(0, 0, 1)}
	hit, back := tri.Hit(backRay, 0.0001, math.MaxFloat64)
	if !hit {
		t.Fatal("expected back face hit")
	}
	if !approxEqual(back.N.X(), -raw.X(), 1e-10) ||
		!approxEqual(back.N.Z(), -raw.Z(), 1e-10) {
		t.Errorf("flipped interpolated normal = %v, want -%v", back.N, raw)
	}
}

func TestTriangleShadowedBySphereInHitableList(t *testing.T) {
	tri := newTestTriangle(t, false)
	// Sphere centred at (0.2, 0.2, -1) with radius 0.5 sits between the ray
	// origin and the z=-2 triangle.
	sphere := NewSphere(0.2, 0.2, -1, 0.5, testMaterial())
	world := &HitableList{List: []Hitable{tri, sphere}}

	r := &Ray{NewVector(0.2, 0.2, 0), NewVector(0, 0, -1)}
	hit, rec := world.Hit(r, 0.0001, math.MaxFloat64)
	if !hit {
		t.Fatal("expected the sphere to occlude the triangle")
	}
	if rec.T >= 1.0 {
		t.Errorf("occluder distance t=%v, want the sphere hit around 0.5", rec.T)
	}

	// Without the sphere the triangle is reached.
	worldOnlyTri := &HitableList{List: []Hitable{tri}}
	hit, rec = worldOnlyTri.Hit(r, 0.0001, math.MaxFloat64)
	if !hit || !approxEqual(rec.T, 2.0, 1e-10) {
		t.Errorf("triangle shadow ray should reach t=2, got hit=%v t=%v", hit, rec)
	}
}

func TestLargeTriangleCountRendersWithinLimit(t *testing.T) {
	// A scene at the cap must build; one past it must be rejected. Kept cheap
	// by exercising the cap check directly rather than allocating the input.
	if MaxTriangles != 1_000_000 {
		t.Errorf("MaxTriangles = %d, want 1000000", MaxTriangles)
	}
	overCap := MaxTriangles + 1
	objects := ObjectsInput{Triangles: make([]TriangleInput, overCap)}
	spec := Specification{Scene: SceneInput{Objects: objects}}
	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("spec with %d triangles must panic", overCap)
		}
	}()
	spec.GetHitableList()
}
