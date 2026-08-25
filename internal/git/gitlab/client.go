package gitlab

import (
	"gitlab.com/gitlab-org/api/client-go/v2"

	"github.com/gwenneg/soundings/internal/config"
)

func NewClient(cfg *config.Config) (*gitlab.Client, error) {
	return gitlab.NewClient(cfg.GitLabToken, gitlab.WithBaseURL(cfg.GitLabBaseURL))
}
