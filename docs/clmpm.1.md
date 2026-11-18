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

  list-backups SOURCE               list the SQL dumps and OpenSearch snapshots in S3 (read-only)
  fetch-backups SOURCE              download and verify the newest of each (-dump -snapshot)
  run-preflights VM                 mail, credentials, DataCite, disk, services stopped
                                    (-set-mail-suppression -set-datacite-safeguards)
  stop-application VM               dry run; -confirm stops the RDM services
  start-application VM              dry run; -confirm starts them
  restore-sql VM SOURCE             dry run; -confirm fetches, checks and loads a dump
  restore-opensearch VM SOURCE      dry run; -confirm fetches, checks and loads a snapshot
                                    (both: -snapshot-first takes a safety snapshot)
  check-index-health VM             the RDM log indices after a load
  show-user-data VM                 where the production data now is
  remove-fetched-backups            dry run; -confirm deletes the downloads on this machine

Launching defaults to Ubuntu 24.04, which the RDM cloud-init files target.
Options go before the arguments. A verb that changes or discards state is a dry
run until it is given -confirm.

EXIT STATUS

  0 ok    1 no such instance, a refused pre-flight, or the state forbids it    2 usage
  65 a download failed verification    66 a named file is missing
  69 multipass, S3 or the VM's services cannot be reached    70 internal error
  75 a wait timed out (retrying may succeed)    78 ~/.clasm is wrong or newer

