clmpm 0.0.12 -- manage Multipass instances for RDM rehearsals

USAGE

  clmpm VERB [-text|-json] [OPTIONS] [ARGUMENTS]
  clmpm -help | -version | -license

VERBS (experimental until 1.0, like clasm's forms: names and output may change)

  list-instances                    show every instance
  show-instance-detail NAME         show one instance's state and resources
  launch-instance-from-cloud-init NAME CLOUD-INIT-FILE
                                    create an instance (-cpus -memory -disk -image)
  start-instance NAME               start a stopped instance
  stop-instance NAME                stop a running instance
  delete-instance NAME              dry run; -confirm deletes, -purge removes for good
  clone-instance SOURCE NAME        copy a stopped instance
  snapshot-instance NAME SNAPSHOT   snapshot a stopped instance (-comment)
  restore-snapshot NAME SNAPSHOT    dry run; -confirm discards current state and restores
  wait-for-cloud-init NAME          wait until cloud-init finishes (-wait -poll)

Launching defaults to Ubuntu 24.04, which the RDM cloud-init files target.
Options go before the arguments. A verb that changes or discards state is a dry
run until it is given -confirm.

EXIT STATUS

  0 ok    1 no such instance, or the state forbids it    2 usage
  66 a named file is missing    69 multipass cannot be run
  70 internal error    75 a wait timed out (retrying may succeed)

