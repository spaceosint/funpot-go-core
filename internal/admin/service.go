package admin

import "strings"

// Service handles admin role checks.
type Service struct {
	allowed map[string]struct{}
}

func NewService(userIDs []string) *Service {
	allowed := make(map[string]struct{}, len(userIDs))
	for _, userID := range userIDs {
		if trimmed := strings.TrimSpace(userID); trimmed != "" {
			allowed[trimmed] = struct{}{}
		}
	}
	return &Service{allowed: allowed}
}

func (s *Service) IsAdmin(userID string) bool {
	if s == nil {
		return false
	}
	_, ok := s.allowed[strings.TrimSpace(userID)]
	return ok
}
