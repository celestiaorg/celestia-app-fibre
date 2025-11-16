package main

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

type stubClient struct {
	cfg       Config
	upCalls   int
	bumpCalls int
	downCalls int
	listCalls int
	err       error
}

func (s *stubClient) Up(context.Context, int) error {
	s.upCalls++
	return s.err
}

func (s *stubClient) Bump(context.Context, int) error {
	s.bumpCalls++
	return s.err
}

func (s *stubClient) Down(context.Context, int) error {
	s.downCalls++
	return s.err
}

func (s *stubClient) List(context.Context) error {
	s.listCalls++
	return s.err
}

func (s *stubClient) GetConfig() Config {
	return s.cfg
}

func (s *stubClient) FindMachineType(context.Context, Provider, string, bool) ([]MachineTypeLocation, error) {
	return nil, s.err
}

func TestMultiClientFanOut(t *testing.T) {
	cfg := Config{}
	do := &stubClient{cfg: cfg}
	gc := &stubClient{cfg: cfg}

	mc := &MultiClient{
		cfg: &cfg,
		clients: map[Provider]Client{
			DigitalOcean: do,
			GoogleCloud:  gc,
		},
		order: []Provider{DigitalOcean, GoogleCloud},
	}

	require.NoError(t, mc.Up(context.Background(), 5))
	require.NoError(t, mc.Bump(context.Background(), 5))
	require.NoError(t, mc.Down(context.Background(), 5))
	require.NoError(t, mc.List(context.Background()))

	require.Equal(t, 1, do.upCalls)
	require.Equal(t, 1, gc.upCalls)
	require.Equal(t, 1, do.bumpCalls)
	require.Equal(t, 1, gc.bumpCalls)
	require.Equal(t, 1, do.downCalls)
	require.Equal(t, 1, gc.downCalls)
	require.Equal(t, 1, do.listCalls)
	require.Equal(t, 1, gc.listCalls)
}

func TestMultiClientAggregatesErrors(t *testing.T) {
	cfg := Config{}
	fail := &stubClient{cfg: cfg, err: errors.New("boom")}
	ok := &stubClient{cfg: cfg}

	mc := &MultiClient{
		cfg: &cfg,
		clients: map[Provider]Client{
			DigitalOcean: ok,
			GoogleCloud:  fail,
		},
		order: []Provider{DigitalOcean, GoogleCloud},
	}

	err := mc.Up(context.Background(), 1)
	require.Error(t, err)
	require.ErrorContains(t, err, "googlecloud")
	require.ErrorContains(t, err, "boom")
}

func TestProvidersInConfig(t *testing.T) {
	cfg := Config{
		Validators: []Instance{
			{Provider: DigitalOcean},
			{Provider: GoogleCloud},
		},
	}

	got := providersInConfig(cfg)
	require.True(t, got[DigitalOcean])
	require.True(t, got[GoogleCloud])
	require.Len(t, got, 2)
}
