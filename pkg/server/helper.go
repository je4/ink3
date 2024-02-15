package server

func emptyIfNil(str *string) string {
	if str == nil {
		return ""
	}
	return *str
}

type size struct {
	Width  int64 `json:"width"`
	Height int64 `json:"height"`
}

func CalcAspectSize(width, height, maxWidth, maxHeight int64) size {
	aspect := float64(width) / float64(height)
	maxAspect := float64(maxWidth) / float64(maxHeight)
	if aspect > maxAspect {
		return size{
			Width:  maxWidth,
			Height: int64(float64(maxWidth) / aspect),
		}
	} else {
		return size{
			Width:  int64(float64(maxHeight) * aspect),
			Height: maxHeight,
		}
	}
}
