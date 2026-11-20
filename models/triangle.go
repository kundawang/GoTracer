package models

import (
	"fmt"
	"math"
)

const (
	// MaxTriangles bounds the number of triangles accepted in a single scene.
	// It protects renders from pathological specs: every ray tests every
	// triangle in O(n) per bounce, so an unbounded list could hang a renderer.
	MaxTriangles = 1_000_000

	// triangleParallelEpsilon: a ray is treated as parallel to the triangle
	// plane when |det| is smaller than this fraction of its scale-invariant
	// maximum |direction| * |edge2 x edge1|.
	triangleParallelEpsilon = 1e-10

	// triangleBaryTolerance keeps edge and vertex boundary intersections
	// inclusive while rejecting points clearly outside the triangle.
	triangleBaryTolerance = 1e-8

	// triangleDegenerateEpsilon: squared area-normal length below which a
	// triangle has effectively zero area and is rejected.
	triangleDegenerateEpsilon = 1e-20

	// triangleNormalEpsilon: an interpolated normal shorter than this falls
	// back to the geometric normal.
	triangleNormalEpsilon = 1e-12
)

// Triangle is a flat triangular surface defined by three vertices in winding
// order. Normals optionally holds one unit normal per vertex; when present the
// hit normal is barycentrically interpolated, otherwise the geometric normal
// of the winding is used. Triangles are double sided: the reported normal is
// always oriented to face the incoming ray, so back faces hit with a flipped
// normal instead of being culled.
type Triangle struct {
	Vertices [3]*Vector
	// Normals holds optional per-vertex normals; every entry is nil when the
	// triangle was created without vertex normals.
	Normals  [3]*Vector
	Material Material

	geometricNormal  *Vector
	areaNormalLength float64
}

// NewTriangle creates a triangle using the geometric normal of its winding.
// It returns an error for non-finite coordinates or degenerate (zero area)
// triangles.
func NewTriangle(v0, v1, v2 *Vector, material Material) (*Triangle, error) {
	return newTriangle([3]*Vector{v0, v1, v2}, [3]*Vector{}, material)
}

// NewTriangleWithNormals creates a triangle with per-vertex normals. All three
// normals must be supplied; zero, missing or non-finite normals are rejected.
func NewTriangleWithNormals(v0, v1, v2, n0, n1, n2 *Vector, material Material) (*Triangle, error) {
	return newTriangle(
		[3]*Vector{v0, v1, v2},
		[3]*Vector{n0, n1, n2},
		material,
	)
}

func newTriangle(verts, norms [3]*Vector, material Material) (*Triangle, error) {
	for i, v := range verts {
		if v == nil || !isFiniteVector(v) {
			return nil, fmt.Errorf(
				"triangle vertex %d is missing or contains a NaN/Infinity coordinate", i)
		}
	}

	edge1 := verts[1].Copy().SubtractVector(verts[0])
	edge2 := verts[2].Copy().SubtractVector(verts[0])
	areaNormal := NewEmptyVector().VectorCrossProduct(edge1, edge2)
	areaNormalLengthSq := areaNormal.SquaredLength()
	if areaNormalLengthSq < triangleDegenerateEpsilon {
		return nil, fmt.Errorf(
			"degenerate triangle: vertices %v, %v, %v have zero area",
			verts[0], verts[1], verts[2])
	}

	hasNormals := false
	for _, n := range norms {
		if n != nil {
			hasNormals = true
			break
		}
	}
	if hasNormals {
		for i, n := range norms {
			if n == nil {
				return nil, fmt.Errorf(
					"triangle normal %d is missing: provide all three vertex normals or none", i)
			}
			if !isFiniteVector(n) {
				return nil, fmt.Errorf(
					"triangle normal %d contains a NaN/Infinity component", i)
			}
			if n.SquaredLength() < triangleNormalEpsilon {
				return nil, fmt.Errorf("triangle normal %d is a zero vector", i)
			}
		}
	}

	return &Triangle{
		Vertices:         verts,
		Normals:          norms,
		Material:         material,
		geometricNormal:  areaNormal.MakeUnitVector(),
		areaNormalLength: math.Sqrt(areaNormalLengthSq),
	}, nil
}

func isFiniteVector(v *Vector) bool {
	for _, x := range v.data {
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return false
		}
	}
	return true
}

// Hit intersects the ray with the triangle using the Moller-Trumbore
// algorithm. The hit interval is open: tmin < t < tmax, matching sphere
// semantics. Barycentric boundaries (edges and vertices) are inclusive within
// triangleBaryTolerance. It returns the hit distance t, the hit point and a
// unit normal that always faces the ray.
func (t *Triangle) Hit(r *Ray, tmin, tmax float64) (bool, *HitRecord) {
	if r.Origin == nil || r.Direction == nil ||
		!isFiniteVector(r.Origin) || !isFiniteVector(r.Direction) {
		return false, nil
	}

	v0 := t.Vertices[0]
	edge1 := t.Vertices[1].Copy().SubtractVector(v0)
	edge2 := t.Vertices[2].Copy().SubtractVector(v0)

	h := NewEmptyVector().VectorCrossProduct(r.Direction, edge2)
	det := edge1.Dot(h)

	// Near-parallel ray: reject with a threshold relative to the scale of the
	// triangle and the ray direction, so no Inf/NaN escapes the division.
	detLimit := triangleParallelEpsilon * r.Direction.Length() * t.areaNormalLength
	if math.Abs(det) < detLimit {
		return false, nil
	}

	invDet := 1.0 / det
	s := r.Origin.Copy().SubtractVector(v0)
	u := invDet * s.Dot(h)
	if u < -triangleBaryTolerance || u > 1.0+triangleBaryTolerance {
		return false, nil
	}

	q := NewEmptyVector().VectorCrossProduct(s, edge1)
	v := invDet * r.Direction.Dot(q)
	if v < -triangleBaryTolerance || u+v > 1.0+triangleBaryTolerance {
		return false, nil
	}

	// Snap near-boundary barycentric coordinates: edge/vertex intersections are
	// inclusive and stay numerically stable.
	if u < 0 {
		u = 0
	} else if u > 1 {
		u = 1
	}
	if v < 0 {
		v = 0
	} else if v > 1 {
		v = 1
	}
	if u+v > 1.0 {
		return false, nil
	}

	hitT := invDet * edge2.Dot(q)
	if hitT <= tmin || hitT >= tmax {
		return false, nil
	}

	return true, &HitRecord{
		T:        hitT,
		P:        r.PointAtParameter(hitT),
		N:        t.surfaceNormal(u, v, r.Direction),
		Material: t.Material,
	}
}

// surfaceNormal returns a unit normal facing the ray, interpolated from the
// vertex normals when available and falling back to the winding normal.
func (t *Triangle) surfaceNormal(u, v float64, rayDirection *Vector) *Vector {
	var n *Vector
	if t.Normals[0] != nil {
		w := 1.0 - u - v
		n = NewVector(
			w*t.Normals[0].X()+u*t.Normals[1].X()+v*t.Normals[2].X(),
			w*t.Normals[0].Y()+u*t.Normals[1].Y()+v*t.Normals[2].Y(),
			w*t.Normals[0].Z()+u*t.Normals[1].Z()+v*t.Normals[2].Z(),
		)
		if n.SquaredLength() < triangleNormalEpsilon {
			n = t.geometricNormal.Copy()
		} else {
			n.MakeUnitVector()
		}
	} else {
		n = t.geometricNormal.Copy()
	}

	if n.Dot(rayDirection) > 0 {
		n.Negate()
	}
	return n
}
