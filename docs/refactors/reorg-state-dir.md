Currently we store state in ~/.local/state/dev

Within that directory we have:

- daemon.log
- worktrees/$project/$slug/$process.log

I'd like to rename that 'worktrees' directory to 'logs' and move daemon log in there so we have:

- logs/daemon.log
- logs/$project/$slug/$process.log

Let's also make the location of this directory configurable via the environment variable DEV_STATE_DIR
