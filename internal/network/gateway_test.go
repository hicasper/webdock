package network

import (
	"context"
	"errors"
	"io"
	"net"
	"reflect"
	"testing"
)

type fakeRunner struct {
	outputs map[string][]byte
	errors  map[string]error
	calls   []string
}

func (f *fakeRunner) Output(_ context.Context, _ string, _ []string, name string, args ...string) ([]byte, error) {
	key := name
	for _, arg := range args {
		key += " " + arg
	}
	f.calls = append(f.calls, key)
	return f.outputs[key], f.errors[key]
}

func (f *fakeRunner) Attach(context.Context, string, []string, io.Reader, io.Writer, io.Writer, string, ...string) error {
	return errors.New("not implemented")
}

func TestDiscoverFiltersAndSortsGateways(t *testing.T) {
	runner := &fakeRunner{
		outputs: map[string][]byte{
			"docker network inspect bridge": []byte(`[{"IPAM":{"Config":[{"Gateway":"172.18.0.1"},{"Gateway":"172.17.0.1"},{"Gateway":"0.0.0.0"},{"Gateway":"172.19.0.1"}]}}]`),
			"docker network inspect extra":  []byte(`[{"IPAM":{"Config":[{"Gateway":"172.17.0.1"}]}}]`),
		},
	}
	local := func() ([]net.IP, error) {
		return []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("172.17.0.1"), net.ParseIP("172.18.0.1")}, nil
	}
	got, warnings := Discover(context.Background(), runner, []string{"bridge", "extra"}, local)
	want := []string{"172.17.0.1", "172.18.0.1"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("gateways = %v, want %v", got, want)
	}
	if len(warnings) != 2 {
		t.Fatalf("warnings = %v, want invalid gateway and non-local gateway warnings", warnings)
	}
}

func TestDiscoverFallsBackWhenDockerUnavailable(t *testing.T) {
	runner := &fakeRunner{errors: map[string]error{
		"docker network inspect bridge": errors.New("docker not found"),
	}}
	got, warnings := Discover(context.Background(), runner, []string{"bridge"}, func() ([]net.IP, error) {
		return nil, nil
	})
	if len(got) != 0 {
		t.Fatalf("gateways = %v, want none", got)
	}
	if len(warnings) != 1 {
		t.Fatalf("warnings = %v", warnings)
	}
}

func TestDiscoverSkipsBridgeWhenLocalAddressesCannotBeVerified(t *testing.T) {
	runner := &fakeRunner{outputs: map[string][]byte{
		"docker network inspect bridge": []byte(`[{"IPAM":{"Config":[{"Gateway":"172.17.0.1"}]}}]`),
	}}
	got, warnings := Discover(context.Background(), runner, []string{"bridge"}, func() ([]net.IP, error) {
		return nil, errors.New("no interfaces")
	})
	if len(got) != 0 || len(warnings) != 1 {
		t.Fatalf("got %v warnings %v", got, warnings)
	}
}
