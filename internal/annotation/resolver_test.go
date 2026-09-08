package annotation

import "github.com/tucats/pdf-viewer/internal/syntax"

// fakeResolver is a minimal in-memory Resolver for tests: objects maps
// an object number to the value Resolve should return for it - the same
// small test double every other internal package in this module defines
// its own copy of (see, for example, internal/image/resolver_test.go).
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
	return s.Raw, nil
}
