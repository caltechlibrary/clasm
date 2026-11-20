# Toy recipe

A five-step recipe for proving the runner on a real host (DR-0202, Phase R1): a read-only
preflight, a step that fails with exit 75 until `/var/tmp/clasm-toy-ok` exists, a 20-second
detached step, a step run as `ubuntu`, and a `changes: database` step that the production
gate refuses without `--confirm`. It changes nothing but that one flag file.

    clmpm run-recipe -apply -confirm VM internal/recipe/testdata/toy-recipe SNAP=x
    clasm rdm-migration run-recipe --apply --confirm INSTANCE internal/recipe/testdata/toy-recipe SNAP=x

It stops at `work`; `touch /var/tmp/clasm-toy-ok` on the host, then resume with `--from work`.
Run it only on a throwaway host. The Go tests do not use it; they build their recipes in code.
