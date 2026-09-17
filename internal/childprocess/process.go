// Package childprocess owns one Wait per external runtime and its descendants.
package childprocess

import (
	"os/exec"
	"sync"
)

type Process struct {
	cmd         *exec.Cmd
	done        chan struct{}
	stop        sync.Once
	release     func()
	releaseOnce sync.Once
}

func Start(cmd *exec.Cmd) (*Process, error) {
	configure(cmd)
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	release, err := contain(cmd)
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, err
	}
	p := &Process{cmd: cmd, done: make(chan struct{}), release: release}
	go func() { _ = cmd.Wait(); p.releaseOnce.Do(p.release); close(p.done) }()
	return p, nil
}
func (p *Process) Alive() bool {
	if p == nil {
		return false
	}
	select {
	case <-p.done:
		return false
	default:
		return true
	}
}
func (p *Process) Done() <-chan struct{} { return p.done }
func (p *Process) Stop() {
	if p == nil {
		return
	}
	p.stop.Do(func() {
		if p.Alive() {
			_ = p.cmd.Process.Kill()
		}
		<-p.done
		p.releaseOnce.Do(p.release)
	})
}
