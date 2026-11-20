package tracer

import (
	"math"
	"math/rand"
	"testing"

	"github.com/DheerendraRathor/GoTracer/models"
)

func seedPtr(v int64) *int64 { return &v }

// triangleScene builds the fixed verification scene: two triangles (a light
// and a matte floor) plus a sphere that occludes part of the floor.
func triangleScene(width, height int) *models.Specification {
	ambient := [3]float64{0.05, 0.05, 0.1}
	return &models.Specification{
		Settings: models.Setting{
			RenderRoutines: 3,
			RenderDepth:    8,
			Seed:           seedPtr(42),
		},
		Image: models.ImageInput{
			OutputFile: "/tmp/gotracer-test.png",
			Height:     height,
			Width:      width,
			Samples:    12,
			Patch:      [4]int{0, height, 0, width},
		},
		Scene: models.SceneInput{
			Camera: models.CameraInput{
				LookFrom:    [3]float64{0, 1.2, 2.5},
				LookAt:      [3]float64{0, 0.5, 0},
				UpVector:    [3]float64{0, 1, 0},
				FieldOfView: 50,
				AspectRatio: float64(width) / float64(height),
				Focus:       2.7,
				Aperture:    0,
			},
			AmbientLight: ambient,
			Objects: models.ObjectsInput{
				Spheres: []models.SphereInput{{
					Center:  [3]float64{0, 0.35, 0.3},
					Radius:  0.35,
					Surface: models.SurfaceInput{Type: models.LambertianMaterial, Albedo: [3]float64{0.05, 0.05, 0.05}},
				}},
				Triangles: []models.TriangleInput{
					{
						// Matte floor triangle, explicit vertex normals (+y).
						Vertices: [][3]float64{{-2.5, 0, -1.5}, {2.5, 0, -1.5}, {0, 0, 2.5}},
						Normals:  [][3]float64{{0, 1, 0}, {0, 1, 0}, {0, 1, 0}},
						Surface:  models.SurfaceInput{Type: models.LambertianMaterial, Albedo: [3]float64{0.7, 0.7, 0.7}},
					},
					{
						// Emissive triangle overhead, geometric normal only.
						Vertices: [][3]float64{{-1.5, 2.2, 0.5}, {1.5, 2.2, 0.5}, {0, 2.2, -1.5}},
						Surface:  models.SurfaceInput{Type: models.LightMaterial, Albedo: [3]float64{12, 12, 12}},
					},
				},
			},
		},
	}
}

func pixelsEqual(a, b *models.Pixel) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.I == b.I && a.J == b.J && a.Color == b.Color
}

func renderPatch(spec *models.Specification, patch [4]int) [][]*models.Pixel {
	clone := *spec
	clone.Image = spec.Image
	clone.Image.Patch = patch
	out := GoTrace(&clone, false, nil, false, nil)
	return out.Pixels
}

func TestSeededRenderIsReproducible(t *testing.T) {
	spec := triangleScene(80, 40)
	first := renderPatch(spec, spec.Image.Patch)
	second := renderPatch(spec, spec.Image.Patch)

	if len(first) != len(second) {
		t.Fatalf("render heights differ: %d vs %d", len(first), len(second))
	}
	for i := range first {
		for j := range first[i] {
			if !pixelsEqual(first[i][j], second[i][j]) {
				t.Fatalf("pixel (%d,%d) differs between seeded renders: %v vs %v",
					i, j, first[i][j], second[i][j])
			}
		}
	}
}

func TestPatchRendersStitchIntoFullRender(t *testing.T) {
	width, height := 80, 40
	spec := triangleScene(width, height)
	full := renderPatch(spec, [4]int{0, height, 0, width})

	// Split the image the way master.go splits work across agents: vertical
	// column patches whose union is the whole image and whose seams do not
	// overlap.
	mid := width / 2
	left := renderPatch(spec, [4]int{0, height, 0, mid})
	right := renderPatch(spec, [4]int{0, height, mid, width})

	// Each patch's output is indexed by row i and the *local* column
	// (j - jmin); the arrays only cover the columns in that patch.
	for i := 0; i < height; i++ {
		for j := 0; j < width; j++ {
			var got *models.Pixel
			if j < mid {
				got = left[i][j]
			} else {
				got = right[i][j-mid]
			}
			if !pixelsEqual(got, full[i][j]) {
				t.Fatalf("stitched pixel (%d,%d) = %v, full render = %v",
					i, j, got, full[i][j])
			}
		}
	}

	// Seam neighbours must both exist and be well-formed pixels.
	if left[0][mid-1] == nil || right[0][0] == nil {
		t.Fatal("patch seam pixels must both be populated")
	}
}

func TestSeedFieldIsOptionalAndKeepsLegacyBehaviour(t *testing.T) {
	spec := triangleScene(24, 12)
	spec.Settings.Seed = nil
	// The legacy path must simply run and produce a fully populated image.
	out := GoTrace(spec, false, nil, false, nil)
	for i := range out.Pixels {
		for j, p := range out.Pixels[i] {
			if p == nil {
				t.Fatalf("missing pixel at (%d,%d) in legacy render", i, j)
			}
		}
	}
}

func TestTriangleReflectionChainHitsLight(t *testing.T) {
	// Deterministic chain exercised through getColor directly: a fuzz-free
	// metal triangle in the z=-1 plane reflects a -z ray straight into an
	// emissive triangle at z=2. Metal fuzz 0 and Light scatter need no
	// randomness, so the returned colour must be exactly the light albedo.
	spec := &models.Specification{
		Settings: models.Setting{RenderRoutines: 1, RenderDepth: 8, Seed: seedPtr(7)},
		Scene: models.SceneInput{
			AmbientLight: [3]float64{0, 0, 0},
			Objects: models.ObjectsInput{
				Triangles: []models.TriangleInput{
					{
						// Mirror in the z=-1 plane, winding normal +z.
						Vertices: [][3]float64{{-1, -1, -1}, {1, -1, -1}, {0, 1, -1}},
						Surface: models.SurfaceInput{
							Type: models.MetalMaterial, Albedo: [3]float64{1, 1, 1}, Fuzz: 0,
						},
					},
					{
						// Light at z=2 covering the reflected +z ray.
						Vertices: [][3]float64{{-1, -1, 2}, {1, -1, 2}, {0, 1, 2}},
						Surface:  models.SurfaceInput{Type: models.LightMaterial, Albedo: [3]float64{8, 8, 8}},
					},
				},
			},
		},
	}
	scene := spec.GetScene()
	rng := rand.New(rand.NewSource(1))

	r := &models.Ray{Origin: models.NewVector(0, 0, 0), Direction: models.NewVector(0, 0, -1)}
	color := getColor(r, scene, 0, rng)
	for c, name := range []string{"X", "Y", "Z"} {
		var got float64
		switch c {
		case 0:
			got = color.X()
		case 1:
			got = color.Y()
		case 2:
			got = color.Z()
		}
		if !approx(got, 8.0) {
			t.Errorf("reflection chain %s = %v, want 8 (light albedo)", name, got)
		}
	}

	// A sphere on the reflected segment breaks the chain: the shadow ray no
	// longer reaches the light and the colour must change.
	spec.Scene.Objects.Spheres = []models.SphereInput{{
		Center:  [3]float64{0, 0, 0.6},
		Radius:  0.4,
		Surface: models.SurfaceInput{Type: models.LambertianMaterial, Albedo: [3]float64{0.1, 0.1, 0.1}},
	}}
	blocked := spec.GetScene()
	blockedColor := getColor(r, blocked, 0, rng)
	if approx(blockedColor.X(), 8.0) && approx(blockedColor.Y(), 8.0) && approx(blockedColor.Z(), 8.0) {
		t.Errorf("occluded reflection chain still returns the light colour %v", blockedColor)
	}
}

func approx(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestTriangleRefractionChainRendersDeterministically(t *testing.T) {
	// A dielectric triangle in the fixed scene exercises the refraction path;
	// the assertion is reproducibility and finite output rather than a colour.
	spec := triangleScene(40, 20)
	spec.Scene.Objects.Triangles[0].Surface = models.SurfaceInput{
		Type:     models.DielectricMaterial,
		Albedo:   [3]float64{1, 1, 1},
		RefIndex: 1.5,
	}
	first := renderPatch(spec, spec.Image.Patch)
	second := renderPatch(spec, spec.Image.Patch)
	for i := range first {
		for j := range first[i] {
			if !pixelsEqual(first[i][j], second[i][j]) {
				t.Fatalf("dielectric render not reproducible at (%d,%d)", i, j)
			}
			if first[i][j] == nil {
				t.Fatalf("missing pixel at (%d,%d)", i, j)
			}
		}
	}
}
