package risk

import "testing"

func TestClassifyFileRisk(t *testing.T) {
	tests := []struct {
		name     string
		filename string
		expected FileRiskLevel
	}{
		// Critical risk files
		{"auth config", "config/auth.go", RiskCritical},
		{"database migration", "db/migrations/001_create_users.sql", RiskCritical},
		{"API spec", "openapi.yaml", RiskCritical},

		// High risk files
		{"dockerfile", "Dockerfile", RiskHigh},
		{"terraform", "infra/main.tf", RiskHigh},
		{"github workflow", ".github/workflows/ci.yml", RiskHigh},

		// Medium risk files (general application code)
		{"service core", "internal/service/processor.go", RiskMedium},
		{"handler outside api dirs", "handlers/users.go", RiskMedium},
		{"regular go file", "internal/utils/helper.go", RiskMedium},
		{"regular python", "src/main.py", RiskMedium},
		{"dependency manifest", "go.mod", RiskMedium},

		// Deep paths - slash globs must match at any depth
		{"nested api dir", "backend/src/api/routes.py", RiskCritical},
		{"nested alembic migration", "services/x/alembic/versions/123_m.py", RiskCritical},
		{"nested deploy dir", "infra/env/prod/deploy/values.yaml", RiskHigh},
		{"nested tests dir", "backend/src/tests/unit/test_api_client.py", RiskLow},

		// Low risk files
		{"test file", "internal/service_test.go", RiskLow},
		{"doc file", "docs/README.md", RiskLow},
		{"generated file", "generated/api.pb.go", RiskLow},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := classifyFileRisk(tt.filename)
			if result != tt.expected {
				t.Errorf("classifyFileRisk(%q) = %v, want %v", tt.filename, result, tt.expected)
			}
		})
	}
}

func TestClassifyFile(t *testing.T) {
	tests := []struct {
		filename string
		expected string
	}{
		{"db/schema.sql", "critical"},
		{"deploy/values.yaml", "high"},
		{"internal/service/processor.go", "medium"},
		{"docs/guide.md", "low"},
	}

	for _, tt := range tests {
		if got := ClassifyFile(tt.filename); got != tt.expected {
			t.Errorf("ClassifyFile(%q) = %q, want %q", tt.filename, got, tt.expected)
		}
	}
}
