package models

import "fmt"

type ObjectType int

const (
	LambertianMaterial = "Lambertian"
	MetalMaterial      = "Metal"
	DielectricMaterial = "Dielectric"
	LightMaterial      = "Light"
)

type ImageInput struct {
	OutputFile string
	Height     int
	Width      int
	Samples    int
	Patch      [4]int
}

func (i *ImageInput) GetPatch() (int, int, int, int) {
	return i.Patch[0], i.Patch[1], i.Patch[2], i.Patch[3]
}

type CameraInput struct {
	LookFrom    [3]float64
	LookAt      [3]float64
	UpVector    [3]float64
	FieldOfView float64
	AspectRatio float64
	Focus       float64
	Aperture    float64
}

type SurfaceInput struct {
	Type     string
	Albedo   [3]float64
	Fuzz     float64
	RefIndex float64
}

func (s *SurfaceInput) getMaterial() Material {

	var material Material

	albedo := NewVectorFromArray(s.Albedo)
	switch s.Type {
	case LambertianMaterial:
		material = NewLambertian(albedo)
	case MetalMaterial:
		material = NewMetal(albedo, s.Fuzz)
	case DielectricMaterial:
		material = NewDielectric(albedo, s.RefIndex)
	case LightMaterial:
		material = NewLight(albedo)
	default:
		panic(fmt.Sprintf("Got invalid surface type: %s", s.Type))
	}

	return material
}

type SphereInput struct {
	Center  [3]float64
	Radius  float64
	Surface SurfaceInput
}

func (s SphereInput) getSphere() *Sphere {
	return NewSphere(s.Center[0], s.Center[1], s.Center[2], s.Radius, s.Surface.getMaterial())
}

// TriangleInput describes a single triangle in the scene spec. Vertices must
// contain exactly three [x, y, z] points in winding order. Normals is optional:
// when present it must contain exactly three unit vectors, one per vertex.
type TriangleInput struct {
	Vertices [][3]float64 `json:"Vertices"`
	Normals  [][3]float64 `json:"Normals,omitempty"`
	Surface  SurfaceInput
}

func (t TriangleInput) getTriangle(index int) (*Triangle, error) {
	if len(t.Vertices) != 3 {
		return nil, fmt.Errorf(
			"triangle %d needs exactly 3 vertices, got %d", index, len(t.Vertices))
	}

	verts := [3]*Vector{
		NewVectorFromArray(t.Vertices[0]),
		NewVectorFromArray(t.Vertices[1]),
		NewVectorFromArray(t.Vertices[2]),
	}

	material := t.Surface.getMaterial()

	if len(t.Normals) == 0 {
		return NewTriangle(verts[0], verts[1], verts[2], material)
	}
	if len(t.Normals) != 3 {
		return nil, fmt.Errorf(
			"triangle %d needs exactly 3 vertex normals, got %d", index, len(t.Normals))
	}

	return NewTriangleWithNormals(
		verts[0], verts[1], verts[2],
		NewVectorFromArray(t.Normals[0]),
		NewVectorFromArray(t.Normals[1]),
		NewVectorFromArray(t.Normals[2]),
		material,
	)
}

type ObjectsInput struct {
	Spheres   []SphereInput
	Triangles []TriangleInput
}

type Setting struct {
	RenderRoutines int
	RenderDepth    int
	// Seed, when set, makes pixel rendering deterministic: every pixel derives
	// its own RNG stream from (Seed, pixel coordinates), so local renders and
	// master/agent patch renders of the same spec produce identical pixels.
	// When nil the renderer keeps its original time-seeded behaviour.
	Seed *int64
}

type SceneInput struct {
	Camera       CameraInput
	Objects      ObjectsInput
	AmbientLight [3]float64
}

type Specification struct {
	Settings Setting
	Image    ImageInput
	Scene    SceneInput
}

type Scene struct {
	Camera       *Camera
	HitableList  *HitableList
	AmbientLight *Vector
}

func (w Specification) GetCamera() *Camera {
	camera := w.Scene.Camera
	return NewCamera(
		NewVectorFromArray(camera.LookFrom),
		NewVectorFromArray(camera.LookAt),
		NewVectorFromArray(camera.UpVector),
		camera.FieldOfView,
		camera.AspectRatio, camera.Aperture, camera.Focus)
}

func (w Specification) GetHitableList() *HitableList {
	world := HitableList{}

	for _, sphere := range w.Scene.Objects.Spheres {
		world.AddHitable(sphere.getSphere())
	}

	if len(w.Scene.Objects.Triangles) > MaxTriangles {
		panic(fmt.Sprintf(
			"scene contains %d triangles but the limit is %d",
			len(w.Scene.Objects.Triangles), MaxTriangles))
	}

	for index, triangle := range w.Scene.Objects.Triangles {
		tri, err := triangle.getTriangle(index)
		if err != nil {
			panic(fmt.Sprintf("invalid triangle %d: %s", index, err.Error()))
		}
		world.AddHitable(tri)
	}

	return &world
}

func (w Specification) GetScene() *Scene {
	return &Scene{
		Camera:       w.GetCamera(),
		HitableList:  w.GetHitableList(),
		AmbientLight: NewVectorFromArray(w.Scene.AmbientLight),
	}
}
