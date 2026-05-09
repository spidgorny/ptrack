package exec

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"ptrack/internal/model"
)

type Invocation struct {
	Command     string
	Argv        []string
	DisplayName string
	Source      model.ProcessSource
	PTY         model.PTYInfo
}

type Runner struct{}

func (Runner) PlanTrackedInvocation(wrapper string, args []string) (Invocation, error) {
	name := filepath.Base(wrapper)
	if !strings.HasPrefix(name, "ptrack") {
		return Invocation{}, errors.New("only ptrack-prefixed commands are trackable")
	}
	if len(args) == 0 {
		return Invocation{}, errors.New("tracked command requires a target argv")
	}

	argv := append([]string(nil), args...)
	return Invocation{
		Command:     argv[0],
		Argv:        argv,
		DisplayName: strings.Join(argv, " "),
		Source:      model.ProcessSource{Kind: "ptrack"},
		PTY:         model.PTYInfo{Enabled: false},
	}, nil
}

func (Runner) PrototypeNotice(inv Invocation) string {
	return fmt.Sprintf("ptrack prototype runtime: tracking %q via singleton daemon without PTY emulation\n", inv.DisplayName)
}
