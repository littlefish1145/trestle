package deps

import "strings"

// PackageQuery is a parsed package search term. Users can search a bare name
// ("fmt"), pin one provider ("vcpkg:fmt"), pin an exact version ("fmt:11.2.0"),
// or pin the provider too ("vcpkg:fmt:11.2.0"). Conan references already carry
// their own version ("fmt/11.1.4"), so a slash query is left as Name alone.
type PackageQuery struct {
	Name     string
	Provider string
	Version  string
}

// ParseQuery splits a raw search term into provider, name and version. It
// returns the term unchanged when there is nothing to split, so plain names
// search as before.
//
// A colon only introduces a version when what precedes it is a package name and
// what follows looks like a version; "vcpkg:fmt" is a provider prefix, and a
// name like "weird:thing" is left alone rather than guessed at.
func ParseQuery(raw string) PackageQuery {
	text := strings.TrimSpace(raw)
	query := PackageQuery{Name: text}
	if text == "" {
		return query
	}
	// Conan references carry the version after a slash and may add
	// @user/channel and [features]; none of that is a name:version pair.
	if strings.ContainsAny(text, "/@[]") {
		return query
	}
	rest := text
	// A leading provider prefix is only a prefix when something follows it.
	if prefix, remainder, found := strings.Cut(rest, ":"); found && remainder != "" {
		switch strings.ToLower(prefix) {
		case "vcpkg", "conan", "manual":
			query.Provider = strings.ToLower(prefix)
			rest = remainder
		}
	}
	name, version, found := strings.Cut(rest, ":")
	if found && name != "" && looksLikeVersion(version) {
		query.Name, query.Version = name, version
		return query
	}
	// No version follows. If a provider prefix was consumed, the remainder is
	// the name; otherwise the whole term is.
	if query.Provider != "" {
		query.Name = rest
	}
	return query
}

// looksLikeVersion keeps a trailing colon segment from being read as a version
// when it is really more of the name, so "weird:thing" stays one token.
func looksLikeVersion(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	// A version starts with a digit: 11.2.0, 2024.1, 1.0-rc1.
	return value[0] >= '0' && value[0] <= '9'
}

// SearchTerm returns the string to hand to a provider: the bare name, with the
// version appended the way conan spells it.
func (query PackageQuery) SearchTerm() string {
	if query.Provider == "conan" && query.Version != "" {
		return query.Name + "/" + query.Version
	}
	return query.Name
}

// Exact reports whether the user pinned a specific version.
func (query PackageQuery) Exact() bool { return query.Version != "" }

// String rebuilds the original term, which the UI echoes back to the user.
func (query PackageQuery) String() string {
	term := query.Name
	if query.Provider != "" && !strings.HasPrefix(term, query.Provider+":") {
		term = query.Provider + ":" + term
	}
	if query.Version != "" && !strings.HasSuffix(term, ":"+query.Version) {
		term += ":" + query.Version
	}
	return term
}
