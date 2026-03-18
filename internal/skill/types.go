package skill

// Tier represents the classification tier for a skill.
type Tier string

// Tier constants define the available skill classification tiers.
const (
	TierCore   Tier = "core"
	TierDomain Tier = "domain"
)

// Skill represents a loaded skill with its metadata and content.
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
