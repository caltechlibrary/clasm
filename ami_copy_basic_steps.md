# Copying an EC2 Instance via AMI

## When to Use This Approach

Use this approach when you have a working, configured EC2 instance that you want to
preserve as a reusable baseline before making significant changes — such as upgrading
software, migrating to a new version, or testing configuration changes. It is
particularly useful when:

- The instance has a complex environment (e.g. Invenio RDM with Docker containers,
  PostgreSQL, and OpenSearch) that would be time-consuming to rebuild from scratch.
- You want a rollback point before applying a version upgrade.
- You need to spin up multiple identical test environments from a known-good state.
- The instance cannot afford downtime but has a low-traffic window suitable for
  snapshotting.

**Important caveat:** `create-image` snapshots the root EBS volume. If your
instance has additional attached EBS volumes (e.g. separate volumes for database
or search data), those must be snapshotted separately.

---

## Stopped vs Running Instances

The `--no-reboot` flag controls whether AWS stops the instance before snapshotting.
It is the right flag to use in both cases described below.

### Stopped instance (preferred when possible)

A stopped instance is the ideal state to clone from. The filesystem is quiesced —
all writes are flushed to disk — so the snapshot is fully consistent.

### Running instance (no downtime)

You can clone a running instance using `--no-reboot`. AWS establishes a
point-in-time view of the EBS volume the moment `create-image` is called, then
copies blocks in the background. The instance keeps running normally throughout.

The result is **crash-consistent**: equivalent to pulling the power cord at the
snapshot moment. For applications like Invenio RDM this means:

| Component | Behaviour on first boot of clone |
|---|---|
| PostgreSQL | Replays WAL automatically — recovers any incomplete transactions |
| OpenSearch | Replays its transaction log — recovers index state |
| Redis | Session/cache data may be lost — ephemeral by design |
| Docker | Container images on disk are unaffected |

For upgrade testing this is acceptable. A few seconds of in-flight transactions
may be missing from the clone, but the clone will boot and recover cleanly.

Heavy write activity (bulk indexing, large imports) during the snapshot window
can slow creation modestly due to increased copy-on-write work, but does not
affect the running instance's performance in a meaningful way.

---

## Step 1: Find Your Instance

List both stopped and running instances:

```bash
aws ec2 describe-instances \
  --filters "Name=instance-state-name,Values=stopped,running" \
  --query "Reservations[*].Instances[*].[InstanceId,State.Name,Tags[?Key=='Name']|[0].Value,ImageId]" \
  --output table
```

Note the `InstanceId` value for the instance you want to clone (e.g. `i-0abc123def456`).

---

## Step 2: Create the AMI

```bash
aws ec2 create-image \
  --instance-id i-0abc123def456 \
  --name "invenio-rdm-baseline-$(date +%Y-%m-%d)" \
  --description "Invenio RDM baseline before version upgrade" \
  --no-reboot \
  --output json
```

- Replace `i-0abc123def456` with your actual instance ID from Step 1.
- `--no-reboot` prevents AWS from stopping the instance before snapshotting.
  Use it for both stopped and running instances.
- The command returns a new `ImageId` immediately (e.g. `ami-0xyz789`).
  The image is created in the background — it is not yet available to launch.
- The `$(date +%Y-%m-%d)` suffix timestamps the image name automatically.

---

## Step 3: Monitor AMI Creation Status

Check status manually:

```bash
aws ec2 describe-images \
  --image-ids ami-0xyz789 \
  --query "Images[*].{ID:ImageId,Name:Name,State:State}" \
  --output table
```

Or block until the AMI is ready (useful inside a script):

```bash
aws ec2 wait image-available --image-ids ami-0xyz789
echo "AMI is ready"
```

`aws ec2 wait` polls every 15 seconds. For large volumes the default timeout
may be reached — in that case use `describe-images` in a loop or check manually.

---

## Timing Estimates

Creation time depends primarily on the size of the EBS root volume. For a running
instance, heavy write activity during the snapshot window may add time.

| Volume size | Typical time       |
|-------------|--------------------|
| < 20 GB     | 5–15 minutes       |
| 20–100 GB   | 15–45 minutes      |
| 100–200 GB  | 45–90 minutes      |
| 200+ GB     | 1.5–3+ hours       |

An Invenio RDM instance with Docker images, PostgreSQL data, and OpenSearch
indices is likely to fall in the 20–60 minute range. The running instance
is unaffected by the snapshot process throughout.

---

## Upgrade Workflow

Once the AMI is available, the typical testing workflow is:

```
running or stopped baseline instance  (--no-reboot, no downtime)
    → create-image → "invenio-rdm-vX-baseline" AMI
                              ↓
                     Launch new test instance
                              ↓
                  PostgreSQL/OpenSearch recover on boot
                              ↓
                     Apply version upgrade
                              ↓
          (keep baseline AMI to roll back if upgrade fails)
```

The original instance remains untouched throughout.
