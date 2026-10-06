# Security Policy

The support images in this repository are handled under the same policy
as [contemper](https://github.com/contemper-project/contemper): see its
[SECURITY.md](https://github.com/contemper-project/contemper/blob/main/SECURITY.md)
for how to report a vulnerability, what to expect (acknowledgement,
assessment and disclosure timelines) and how reporters are credited.

## Reporting a vulnerability

Please report security vulnerabilities privately, using GitHub's private
vulnerability reporting: open the "Security" tab on this repository and
click "Report a vulnerability", or go directly to
<https://github.com/contemper-project/contemper/security/advisories/new>
and name the affected image. Do not open a public issue for a suspected
vulnerability. If you can't use GitHub's form, follow the fallback in
contemper's policy.

## Supported versions

Only the latest release of each image is supported. Fixes land there;
please upgrade before reporting an issue that may already be fixed.

## Scope

A support image is a small set of files (scripts, service definitions,
udev rules) that contemper merges into an image it converts. These files
run as root inside the guest. In scope:

- a flaw in a script or service definition shipped in an image, such as
  unsafe handling of input, files or permissions;
- a flaw in the image build tooling that could let a published image
  differ from the files in this repository;
- a flaw in the release workflows that could let someone else publish an
  image under the project's name.

Vulnerabilities in the software a support image integrates with (for
example an agent that the image starts) are out of scope for this
repository; please report those to the software's own project. Files
copied from another project keep that project's behaviour, and their
origin is recorded in [NOTICE](NOTICE).
