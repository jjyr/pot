package control

func (s *Service) ControlRole() string {
	if s.options.Embedded {
		return "embedded"
	}
	return s.Machines.Role()
}
