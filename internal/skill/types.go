package skill

type Tier string

const (
	TierCore   Tier = "core"
	TierDomain Tier = "domain"
)

type Skill struct {
	Name          string   `yaml:"name"`
	Description   string   `yaml:"description"`
	Category      string   `yaml:"category"`
	Tier          Tier     `yaml:"tier"`
	WhenToUse     string   `yaml:"when_to_use"`
	RelatedSkills []string `yaml:"related_skills"`
	Content       string
	FilePath      string
}
