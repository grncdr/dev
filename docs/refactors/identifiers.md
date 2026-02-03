we currently have identifiers like this:

- $project/$slug:$process to canonically identify one process

We implicitly resolve the $project/$slug` part from the callers current working directory.

We also have commands that accept a slug as an argument, which can also be provided as `$project/$slug`.

We restrict project and slug names to valid DNS labels *but* it would be nice to support slashes in slugs because they often map 1:1 with git branches (e.g. feature/my-cool-thing).

Let's change the identifier format to $project:$slug:$process, allow slashes in slugs, and replace slashes in slugs with a hyphen when creating domains. The 'worktree add' subcommand should validate that the normalized form of a slug doesn't conflict with any existing slugs.

