package main

import (
	"encoding/json"
	"net/url"
	"os"
)

type state struct {
	path    string
	Volumes map[string]volumeState
}

type volumeState struct {
	MountPath   string
	StagingPath string

	StageHookURL   *url.URL
	UnstageHookURL *url.URL

	Variables map[string]string
}

func readState(path string) (*state, error) {
	s := &state{
		path:    path,
		Volumes: make(map[string]volumeState),
	}
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			err = s.save()
			if err != nil {
				return nil, err
			}
			return s, nil
		}
		return nil, err
	}
	defer f.Close()
	err = json.NewDecoder(f).Decode(s)
	if err != nil {
		return nil, err
	}
	return s, nil
}

func (s *state) save() error {
	f, err := os.Create(s.path)
	if err != nil {
		return err
	}
	defer f.Close()
	return json.NewEncoder(f).Encode(s)
}
