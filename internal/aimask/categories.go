package aimask

// Photographer-facing categories for semantic class masks. The segmentation
// model speaks ADE20K's 150-class research vocabulary (swivel chair, arcade
// machine, …); masks speak this table's ~10 categories. The mapping is baked
// into generated class maps (map pixel = category ID), so editing this table
// must bump the class model's MapVer — otherwise stale maps keep the old
// grouping. Category IDs are stable API: they live in saved edit params.
const (
	CatOther        = 0 // uncategorized ADE20K classes; never offered as a mask
	CatSky          = 1
	CatPeople       = 2
	CatFoliage      = 3
	CatWater        = 4
	CatGround       = 5
	CatArchitecture = 6
	CatMountains    = 7
	CatVehicles     = 8
	CatAnimals      = 9
)

// CategoryNames indexes UI labels by category ID. The client mirrors these
// labels; order and IDs must not change (see the table comment above).
var CategoryNames = []string{
	CatOther:        "Other",
	CatSky:          "Sky",
	CatPeople:       "People",
	CatFoliage:      "Foliage",
	CatWater:        "Water",
	CatGround:       "Ground",
	CatArchitecture: "Architecture",
	CatMountains:    "Mountains & rocks",
	CatVehicles:     "Vehicles",
	CatAnimals:      "Animals",
}

// ade20kCategory maps ADE20K class indexes (0..149, the standard order) to
// category IDs. Unlisted classes stay CatOther.
var ade20kCategory = func() [150]uint8 {
	var t [150]uint8
	assign := func(cat uint8, classes ...int) {
		for _, c := range classes {
			t[c] = cat
		}
	}
	assign(CatSky, 2)
	assign(CatPeople, 12)
	assign(CatFoliage, 4 /*tree*/, 9 /*grass*/, 17 /*plant*/, 29 /*field*/, 66 /*flower*/, 72 /*palm*/)
	assign(CatWater, 21 /*water*/, 26 /*sea*/, 60 /*river*/, 104 /*fountain*/, 109 /*swimming pool*/, 113 /*waterfall*/, 128 /*lake*/)
	assign(CatGround, 3 /*floor*/, 6 /*road*/, 11 /*sidewalk*/, 13 /*earth*/, 46 /*sand*/, 52 /*path*/, 54 /*runway*/, 91 /*dirt track*/, 94 /*land*/)
	assign(CatArchitecture,
		0 /*wall*/, 1 /*building*/, 5 /*ceiling*/, 8 /*windowpane*/, 14 /*door*/, 25, /*house*/
		32 /*fence*/, 38 /*railing*/, 42 /*column*/, 48 /*skyscraper*/, 51 /*grandstand*/, 53, /*stairs*/
		59 /*stairway*/, 61 /*bridge*/, 79 /*hovel*/, 84 /*tower*/, 86 /*awning*/, 88, /*booth*/
		95 /*bannister*/, 96 /*escalator*/, 101 /*stage*/, 114 /*tent*/, 121 /*step*/)
	assign(CatMountains, 16 /*mountain*/, 34 /*rock*/, 68 /*hill*/)
	assign(CatVehicles, 20 /*car*/, 76 /*boat*/, 80 /*bus*/, 83 /*truck*/, 90 /*airplane*/, 102 /*van*/, 103 /*ship*/, 116 /*minibike*/, 127 /*bicycle*/)
	assign(CatAnimals, 126 /*animal*/)
	return t
}()

// CategoryPlane collapses an ADE20K class-index plane (argmax output) into a
// category map — the post-processing step for the class model when it lands.
func CategoryPlane(classes []uint8) []uint8 {
	out := make([]uint8, len(classes))
	for i, c := range classes {
		if int(c) < len(ade20kCategory) {
			out[i] = ade20kCategory[c]
		}
	}
	return out
}

// Category reports one detected category and how much of the frame it covers.
type Category struct {
	ID       int     `json:"id"`
	Name     string  `json:"name"`
	Fraction float64 `json:"fraction"`
}

// detectionMinFraction filters noise: a category below ~1.5% of the frame is
// not worth offering as a mask.
const detectionMinFraction = 0.015

// DetectCategories lists the categories present in a category map, largest
// first, skipping CatOther and sub-threshold slivers.
func DetectCategories(catPlane []uint8) []Category {
	if len(catPlane) == 0 {
		return nil
	}
	counts := make([]int, len(CategoryNames))
	for _, c := range catPlane {
		if int(c) < len(counts) {
			counts[c]++
		}
	}
	var out []Category
	for id := 1; id < len(counts); id++ {
		frac := float64(counts[id]) / float64(len(catPlane))
		if frac >= detectionMinFraction {
			out = append(out, Category{ID: id, Name: CategoryNames[id], Fraction: frac})
		}
	}
	for i := 1; i < len(out); i++ { // insertion sort, largest first (≤9 items)
		for j := i; j > 0 && out[j].Fraction > out[j-1].Fraction; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}
