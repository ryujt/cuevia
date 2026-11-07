# Select a public certificate fingerprint from security find-identity -v output.
# Existing bundle authorities take precedence over a fresh local development build.
BEGIN { FS = "\"" }

{
  prefix = $1
  sub(/^[[:space:]]+/, "", prefix)
  split(prefix, fields, /[[:space:]]+/)
  fingerprint = toupper(fields[2])
  name = $2
  if (fields[1] !~ /^[0-9]+\)$/ || length(fingerprint) != 40 ||
      fingerprint !~ /^[0-9A-F]+$/ || name !~ /^(Apple Development:|Developer ID Application:)/) next
  if (fingerprint in names) next
  names[fingerprint] = name
  count++
  only_identity = fingerprint
  if (name ~ /^Apple Development:/) {
    development_count++
    development_identity = fingerprint
  }
}

function ambiguous() {
  print "Multiple signing identities remain. Set CUEVIA_SIGN_IDENTITY to a certificate name or SHA-1." > "/dev/stderr"
  print "Use security find-identity -v -p codesigning to list available identities." > "/dev/stderr"
  exit 1
}

function identity_for_authority(authority, fingerprint, matches, selected) {
  if (authority == "") return ""
  for (fingerprint in names) {
    if (names[fingerprint] == authority) {
      matches++
      selected = fingerprint
    }
  }
  if (matches > 1) ambiguous()
  return selected
}

END {
  if (count == 0) {
    print "No valid Apple Development or Developer ID Application signing identities were found." > "/dev/stderr"
    print "Use security find-identity -v -p codesigning to list available identities." > "/dev/stderr"
    exit 1
  }
  if (count == 1) {
    print only_identity
    exit
  }
  selected = identity_for_authority(installed_authority)
  if (selected == "") selected = identity_for_authority(built_authority)
  if (selected != "") {
    print selected
    exit
  }
  if (development_count == 1) {
    print development_identity
    exit
  }
  ambiguous()
}
