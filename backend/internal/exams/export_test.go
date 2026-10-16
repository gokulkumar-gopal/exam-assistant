package exams

// SetMaxPending lowers the safety bound on unfinished attempt rows for tests.
func (s *Service) SetMaxPending(n int) { s.maxPend = n }
