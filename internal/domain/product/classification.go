package product

import "errors"

var (
	// ErrUnknownHandlingTag is returned when a tag is not a member of the closed set.
	ErrUnknownHandlingTag = errors.New("unknown handling tag")
	// ErrUnknownTemperatureClass is returned when a temperature class is not a member of the closed set.
	ErrUnknownTemperatureClass = errors.New("unknown temperature class")
	// ErrNoHandlingTags is returned when a classification has no tag at all.
	ErrNoHandlingTags = errors.New("classification requires at least one handling tag")
	// ErrDuplicateHandlingTag is returned when a tag appears twice: the tags are a set.
	ErrDuplicateHandlingTag = errors.New("duplicate handling tag")
	// ErrTemperatureClassRequired is returned when TemperatureSensitive has no temperature class.
	ErrTemperatureClassRequired = errors.New("temperature-sensitive classification requires a temperature class")
	// ErrTemperatureClassNotApplicable is returned when a temperature class is given without TemperatureSensitive.
	ErrTemperatureClassNotApplicable = errors.New("temperature class is only meaningful when the temperature-sensitive tag is present")
	// ErrInvalidDOTHazardClass is returned when a DOT hazard class is outside 1..9.
	ErrInvalidDOTHazardClass = errors.New("dot hazard class must be between 1 and 9")
	// ErrDOTHazardClassNotApplicable is returned when a DOT hazard class is given without Hazmat.
	ErrDOTHazardClassNotApplicable = errors.New("dot hazard class is only meaningful when the hazmat tag is present")
	// ErrUnknownClassificationSource is returned when a source is neither native nor legacy-import.
	ErrUnknownClassificationSource = errors.New("unknown classification source")
)

// HandlingTag is one member of the closed set of ways a SKU must be handled.
type HandlingTag string

// The closed set of handling tags, in their stable order.
const (
	Hazmat               HandlingTag = "Hazmat"
	Fragile              HandlingTag = "Fragile"
	TemperatureSensitive HandlingTag = "TemperatureSensitive"
	Oversized            HandlingTag = "Oversized"
	HighValue            HandlingTag = "HighValue"
)

// tagOrder is the stable order tags are reported in (enum declaration order).
var tagOrder = []HandlingTag{Hazmat, Fragile, TemperatureSensitive, Oversized, HighValue}

// ParseHandlingTag validates a tag against the closed set.
func ParseHandlingTag(value string) (HandlingTag, error) {
	for _, tag := range tagOrder {
		if string(tag) == value {
			return tag, nil
		}
	}
	return "", ErrUnknownHandlingTag
}

// TemperatureClass is the storage temperature band a TemperatureSensitive
// product requires. Empty means "none".
type TemperatureClass string

// The closed set of temperature classes.
const (
	NoTemperatureClass TemperatureClass = ""
	Ambient            TemperatureClass = "Ambient"
	Chilled            TemperatureClass = "Chilled"
	Frozen             TemperatureClass = "Frozen"
)

// ParseTemperatureClass validates a non-empty temperature class.
func ParseTemperatureClass(value string) (TemperatureClass, error) {
	switch TemperatureClass(value) {
	case Ambient, Chilled, Frozen:
		return TemperatureClass(value), nil
	}
	return NoTemperatureClass, ErrUnknownTemperatureClass
}

// DOTHazardClass is a top-level US DOT hazard class, 1..9. Zero means
// "not recorded", which is valid even for a Hazmat product (inherited from
// inventory-storage ADR 0010).
type DOTHazardClass int

// NoDOTHazardClass is the "not recorded" value.
const NoDOTHazardClass DOTHazardClass = 0

// ClassificationSource records where a classification was authored.
type ClassificationSource string

// The classification sources (ADR 0003).
const (
	SourceNative       ClassificationSource = "native"
	SourceLegacyImport ClassificationSource = "legacy-import"
)

// ParseClassificationSource validates a source.
func ParseClassificationSource(value string) (ClassificationSource, error) {
	switch ClassificationSource(value) {
	case SourceNative, SourceLegacyImport:
		return ClassificationSource(value), nil
	}
	return "", ErrUnknownClassificationSource
}

// Classification is the value object describing how a SKU must be handled.
// It is immutable; construct it with NewClassification.
type Classification struct {
	tags             map[HandlingTag]struct{}
	temperatureClass TemperatureClass
	dotHazardClass   DOTHazardClass
}

// NewClassification validates the closed taxonomy and its invariants:
// at least one tag, no duplicates, TemperatureClass iff TemperatureSensitive,
// DOTHazardClass (when non-zero) in 1..9 and only with Hazmat.
func NewClassification(tags []HandlingTag, temperatureClass TemperatureClass, dotHazardClass DOTHazardClass) (Classification, error) {
	if len(tags) == 0 {
		return Classification{}, ErrNoHandlingTags
	}
	set := make(map[HandlingTag]struct{}, len(tags))
	for _, tag := range tags {
		if _, err := ParseHandlingTag(string(tag)); err != nil {
			return Classification{}, err
		}
		if _, dup := set[tag]; dup {
			return Classification{}, ErrDuplicateHandlingTag
		}
		set[tag] = struct{}{}
	}
	if err := checkTemperature(set, temperatureClass); err != nil {
		return Classification{}, err
	}
	if err := checkDOT(set, dotHazardClass); err != nil {
		return Classification{}, err
	}
	return Classification{tags: set, temperatureClass: temperatureClass, dotHazardClass: dotHazardClass}, nil
}

func checkTemperature(set map[HandlingTag]struct{}, temperatureClass TemperatureClass) error {
	_, sensitive := set[TemperatureSensitive]
	if !sensitive {
		if temperatureClass != NoTemperatureClass {
			return ErrTemperatureClassNotApplicable
		}
		return nil
	}
	if temperatureClass == NoTemperatureClass {
		return ErrTemperatureClassRequired
	}
	_, err := ParseTemperatureClass(string(temperatureClass))
	return err
}

func checkDOT(set map[HandlingTag]struct{}, dotHazardClass DOTHazardClass) error {
	if dotHazardClass == NoDOTHazardClass {
		return nil
	}
	if _, hazmat := set[Hazmat]; !hazmat {
		return ErrDOTHazardClassNotApplicable
	}
	if dotHazardClass < 1 || dotHazardClass > 9 {
		return ErrInvalidDOTHazardClass
	}
	return nil
}

// Tags returns the tags in the stable order (Hazmat, Fragile,
// TemperatureSensitive, Oversized, HighValue).
func (c Classification) Tags() []HandlingTag {
	out := make([]HandlingTag, 0, len(c.tags))
	for _, tag := range tagOrder {
		if _, ok := c.tags[tag]; ok {
			out = append(out, tag)
		}
	}
	return out
}

// HasTag reports whether the classification carries tag.
func (c Classification) HasTag(tag HandlingTag) bool {
	_, ok := c.tags[tag]
	return ok
}

// TemperatureClass returns the required band, or NoTemperatureClass.
func (c Classification) TemperatureClass() TemperatureClass { return c.temperatureClass }

// DOTHazardClass returns the recorded DOT hazard class, or NoDOTHazardClass.
func (c Classification) DOTHazardClass() DOTHazardClass { return c.dotHazardClass }

// Equal reports whether two classifications describe the same handling.
func (c Classification) Equal(other Classification) bool {
	if len(c.tags) != len(other.tags) || c.temperatureClass != other.temperatureClass || c.dotHazardClass != other.dotHazardClass {
		return false
	}
	for tag := range c.tags {
		if _, ok := other.tags[tag]; !ok {
			return false
		}
	}
	return true
}
