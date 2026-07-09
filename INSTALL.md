Installation **clasm**
============================

**clasm** Interactive Go CLI (clasm) for administering Caltech Library DLD's AWS EC2 instances, AMIs, and S3 backup archives, with support for tag management, cloud-init inspection, and backup archival to S3.

Installing from source
----------------------

### Required software

- Go >= 1.26
- CMTools >= 0.0.46

### Steps

1. git clone https://github.com/caltechlibrary/clasm
2. Change directory into the `clasm` directory
3. Make to build, test and install

~~~shell
git clone https://github.com/caltechlibrary/clasm
cd clasm
make
make test
make install
~~~

