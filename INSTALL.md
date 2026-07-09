Installation **clasm**
============================

**clasm** Interactive Go CLI (clasm) for administering Caltech Library DLD's AWS EC2 instances, AMIs, and S3 backup archives, with support for tag management, cloud-init inspection, and backup archival to S3.

Quick install with curl or irm
------------------------------

There is an experimental installer.sh script that can be run with the following command to install latest table release. This may work for macOS, Linux and if you’re using Windows with the Unix subsystem. This would be run from your shell (e.g. Terminal on macOS).

~~~shell
curl https://caltechlibrary.github.io/clasm/installer.sh | sh
~~~

This will install the programs included in clasm in your `$HOME/bin` directory.

If you are running Windows 10 or 11 use the Powershell command below.

~~~ps1
irm https://caltechlibrary.github.io/clasm/installer.ps1 | iex
~~~

### If your are running macOS or Windows

You may get security warnings if you are using macOS or Windows. See the notes for the specific operating system you’re using to fix issues.

- [INSTALL_NOTES_macOS.md](INSTALL_NOTES_macOS.md)
- [INSTALL_NOTES_Windows.md](INSTALL_NOTES_Windows.md)

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

