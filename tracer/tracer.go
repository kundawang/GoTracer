package tracer

import (
	"math"
	"math/rand"
	"runtime"
	"sync"
	"time"

	"github.com/DheerendraRathor/GoTracer/models"
)

var MaxRenderDepth int = 10

type TracerOutput struct {
	Pixels [][]*models.Pixel
}

func GoTrace(
	env *models.Specification,
	sharePixelProgress bool, progress chan<- *models.Pixel,
	isClosable bool, closeChan <-chan bool,
) *TracerOutput {
	if env.Settings.RenderDepth > 0 {
		MaxRenderDepth = env.Settings.RenderDepth
	}

	scene := env.GetScene()

	width, height := env.Image.Width, env.Image.Height

	askedRenderRoutines := env.Settings.RenderRoutines
	if askedRenderRoutines <= 0 {
		askedRenderRoutines = runtime.NumCPU()
	}

	renderRoutines := askedRenderRoutines - 2
	if renderRoutines < 1 {
		renderRoutines = 1
	}

	processingGroupData := make([][][2]int, renderRoutines)
	for i := range processingGroupData {
		// Start empty: a preallocated capacity would leave zero-valued
		// [2]int{0,0} entries that re-render pixel (0,0) once per goroutine
		// and corrupt the pixel stream sent to distributed agents.
		processingGroupData[i] = make([][2]int, 0)
	}

	imin, imax, jmin, jmax := env.Image.GetPatch()

	var output *TracerOutput
	if !sharePixelProgress {
		// Size the array to the patch itself, not the global image bounds:
		// a patch starting at non-zero imin/jmin would otherwise store its
		// rows and columns at the wrong local indices and leave leading gaps.
		rows := imax - imin
		cols := jmax - jmin
		output = &TracerOutput{
			Pixels: make([][]*models.Pixel, rows),
		}
		for i := range output.Pixels {
			output.Pixels[i] = make([]*models.Pixel, cols)
		}
	}

	division := 0
	processingGroup := 0
	for i := imax - 1; i >= imin; i-- {
		for j := jmin; j < jmax; j++ {

			processingGroup = division % renderRoutines
			processingGroupData[processingGroup] = append(processingGroupData[processingGroup], [2]int{i, j})
			division += 1

			/*
				// Check if go tracer is asked for close
				if isClosable {
					select {
					case <-closeChan:
						break IMAGE_PROCESSING
					default:
					}
				}
			*/

		}
	}

	var renderWg sync.WaitGroup
	renderWg.Add(renderRoutines)
	for _, _data := range processingGroupData {
		go func(samples int, scene *models.Scene, data [][2]int, seed *int64) {

			defer func() {
				renderWg.Done()
			}()

			var rng *rand.Rand
			if seed == nil {
				rng = rand.New(rand.NewSource(time.Now().Unix() + rand.Int63()))
			}

			for _, point := range data {
				i, j := point[0], point[1]
				pixelRng := rng
				if seed != nil {
					// One independent stream per pixel coordinate: identical
					// specs and patches always reproduce the same pixel, on a
					// local render or on any agent.
					pixelRng = rand.New(rand.NewSource(pixelSeed(*seed, i, j)))
				}
				pixel := processPixel(i, j, width, height, samples, scene, pixelRng)
				if sharePixelProgress {
					progress <- pixel
				} else {
					// Store at patch-local indices; the Pixel itself keeps its
					// global image coordinates for the final PNG placement.
					output.Pixels[i-imin][j-jmin] = pixel
				}
			}
		}(env.Image.Samples, scene, _data, env.Settings.Seed)
	}

	renderWg.Wait()

	if sharePixelProgress {
		progress <- nil
	}

	return output
}

func processPixel(i, j, imageWidth, imageHeight, sample int, scene *models.Scene, rng *rand.Rand) *models.Pixel {
	pixel := models.NewEmptyVector()
	for s := 0; s < sample; s++ {
		randFloatu, randFloatv := rng.Float64(), rng.Float64()
		u, v := (float64(j)+randFloatu)/float64(imageWidth), (float64(i)+randFloatv)/float64(imageHeight)
		ray := scene.Camera.RayAt(u, v, rng)
		pixel.AddVector(getColor(ray, scene, 0, rng))
	}

	pixel.Scale(1 / float64(sample)).Gamma2()
	uint8Pixel := pixel.ToPixel(j, imageHeight-i-1)
	return uint8Pixel
}

func getColor(r *models.Ray, scene *models.Scene, renderDepth int, rng *rand.Rand) *models.Vector {

	// tmin is 0.0001 to avoid self intersection
	didHit, hitRecord := scene.HitableList.Hit(r, 0.0001, math.MaxFloat64)
	if didHit {
		shouldScatter, attenuation, ray := hitRecord.Material.Scatter(r, hitRecord, rng)

		if hitRecord.Material.IsLight() {
			return attenuation
		}

		if renderDepth < MaxRenderDepth && shouldScatter {
			return attenuation.MultiplyVector(getColor(ray, scene, renderDepth+1, rng))
		} else {
			return models.NewEmptyVector()
		}
	}

	return scene.AmbientLight
}

// pixelSeed derives a deterministic RNG seed for one pixel from the scene seed
// and its coordinates. The splitmix64-style finalizer spreads neighbouring
// coordinates into uncorrelated streams and only depends on integer math, so
// the same pixel is reproduced bit-for-bit on every machine.
func pixelSeed(sceneSeed int64, i, j int) int64 {
	z := uint64(sceneSeed) ^ uint64(uint32(i))*0x9e3779b97f4a7c15 ^
		uint64(uint32(j))*0xc2b2ae3d27d4eb4f
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	z = z ^ (z >> 31)
	return int64(z)
}
