package image

import (
	"github.com/tucats/pdf-viewer/internal/filter"
	"github.com/tucats/pdf-viewer/internal/syntax"
)

// fakeResolver is a minimal, in-memory Resolver for tests in this
// package: object numbers are looked up in a plain map rather than
// requiring a real parsed PDF file, and DecodeStream/ResolveDictionary
// are implemented directly against internal/filter and this same map,
// mirroring exactly what internal/parser.Document actually does (see
// resolve.go there) closely enough that a test built against
// fakeResolver exercises the same contract this package's real caller
// relies on.
type fakeResolver struct {
	objects map[int]syntax.Object
}

func (f *fakeResolver) Resolve(num int) (syntax.Object, error) {
	if obj, ok := f.objects[num]; ok {
		return obj, nil
	}
	return syntax.Null{}, nil
}

func (f *fakeResolver) ResolveDictionary(dict syntax.Dictionary) (syntax.Dictionary, error) {
	out := make(syntax.Dictionary, len(dict))
	for k, v := range dict {
		rv, err := resolveIfRef(f, v)
		if err != nil {
			return nil, err
		}
		out[k] = rv
	}
	return out, nil
}

func (f *fakeResolver) DecodeStream(s syntax.Stream) ([]byte, error) {
	dict, err := f.ResolveDictionary(s.Dict)
	if err != nil {
		return nil, err
	}
	return filter.Decode(dict, s.Raw)
}
