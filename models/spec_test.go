package models

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestTriangleInputParsing(t *testing.T) {
	raw := `{
	  "Camera": {"LookFrom": [0,0,1], "LookAt": [0,0,0], "UpVector": [0,1,0],
	             "FieldOfView": 60, "AspectRatio": 1, "Focus": 1, "Aperture": 0},
	  "Scene": {
	    "AmbientLight": [0.1, 0.1, 0.1],
	    "Objects": {
	      "Triangles": [
	        {"Vertices": [[0,0,-1],[1,0,-1],[0,1,-1]],
	         "Normals": [[0,0,1],[0,0,1],[0,0,1]],
	         "Surface": {"Type": "Metal", "Albedo": [0.8,0.8,0.8], "Fuzz": 0.1}},
	        {"Vertices": [[0,0,-2],[2,0,-2],[0,2,-2]],
	         "Surface": {"Type": "Lambertian", "Albedo": [0.2,0.3,0.4]}}
	      ]
	    }
	  }
	}`

	var spec Specification
	if err := json.Unmarshal([]byte(raw), &spec); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if len(spec.Scene.Objects.Triangles) != 2 {
		t.Fatalf("expected 2 triangles, got %d", len(spec.Scene.Objects.Triangles))
	}

	world := spec.GetHitableList()
	if len(world.List) != 2 {
		t.Fatalf("expected 2 hitables, got %d", len(world.List))
	}
	tri0, ok := world.List[0].(*Triangle)
	if !ok {
		t.Fatalf("first hitable is %T, want *Triangle", world.List[0])
	}
	if tri0.Normals[0] == nil {
		t.Error("first triangle should carry vertex normals")
	}
	tri1 := world.List[1].(*Triangle)
	if tri1.Normals[0] != nil {
		t.Error("second triangle should use geometric normals")
	}
	if _, ok := tri0.Material.(*Metal); !ok {
		t.Errorf("triangle 0 material = %T, want *Metal", tri0.Material)
	}
	if _, ok := tri1.Material.(*Lambertian); !ok {
		t.Errorf("triangle 1 material = %T, want *Lambertian", tri1.Material)
	}
}

func TestSphereSpecStillBuildsUnchanged(t *testing.T) {
	raw := `{
	  "Scene": {
	    "Objects": {
	      "Spheres": [
	        {"Center": [0,0,-1], "Radius": 0.5,
	         "Surface": {"Type": "Lambertian", "Albedo": [0.8,0.1,0.1]}}
	      ]
	    }
	  }
	}`
	var spec Specification
	if err := json.Unmarshal([]byte(raw), &spec); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	world := spec.GetHitableList()
	if len(world.List) != 1 {
		t.Fatalf("expected 1 sphere hitable, got %d", len(world.List))
	}
	if _, ok := world.List[0].(*Sphere); !ok {
		t.Errorf("hitable = %T, want *Sphere", world.List[0])
	}
}

func TestInvalidTriangleSpecPanicsWithMessage(t *testing.T) {
	base := func(verts [][3]float64, normals [][3]float64) Specification {
		return Specification{
			Scene: SceneInput{
				Objects: ObjectsInput{
					Triangles: []TriangleInput{{
						Vertices: verts,
						Normals:  normals,
						Surface: SurfaceInput{
							Type:   LambertianMaterial,
							Albedo: [3]float64{0.5, 0.5, 0.5},
						},
					}},
				},
			},
		}
	}

	cases := map[string]Specification{
		"wrong vertex count": base(
			[][3]float64{{0, 0, -1}, {1, 0, -1}}, nil),
		"wrong normal count": base(
			[][3]float64{{0, 0, -1}, {1, 0, -1}, {0, 1, -1}},
			[][3]float64{{0, 0, 1}, {0, 0, 1}}),
		"degenerate": base(
			[][3]float64{{0, 0, -1}, {1, 0, -1}, {2, 0, -1}}, nil),
	}

	for name, spec := range cases {
		t.Run(name, func(t *testing.T) {
			defer func() {
				r := recover()
				if r == nil {
					t.Fatal("expected panic for invalid triangle spec")
				}
				if msg, ok := r.(string); !ok || !strings.Contains(msg, "triangle") {
					t.Fatalf("panic message should mention the triangle, got: %v", r)
				}
			}()
			spec.GetHitableList()
		})
	}
}
