package control

func (s *Service) HasSandbox() bool {
	return s.options.Sandboxes != nil && len(s.options.Sandboxes.Descriptors()) > 0
}
func (s *Service) ControlRole() string {
	if s.options.Embedded {
		return "embedded"
	}
	return s.Machines.Role()
}
