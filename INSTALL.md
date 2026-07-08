Installation **awstools**
============================

**awstools** is an interactive Go CLI (`awsops`) for managing our RDM collection of AMI and EC2 instances.

Quick install with curl or irm
------------------------------

There is an experimental installer.sh script that can be run with the following command to install latest table release. This may work for macOS, Linux and if you’re using Windows with the Unix subsystem. This would be run from your shell (e.g. Terminal on macOS).

~~~shell
curl https://caltechlibrary.github.io/awstools/installer.sh | sh
~~~

This will install the programs included in awstools in your `$HOME/bin` directory.

If you are running Windows 10 or 11 use the Powershell command below.

~~~ps1
irm https://caltechlibrary.github.io/awstools/installer.ps1 | iex
~~~

### If your are running macOS or Windows

You may get security warnings if you are using macOS or Windows. See the notes for the specific operating system you’re using to fix issues.

- [INSTALL_NOTES_macOS.md](INSTALL_NOTES_macOS.md)
- [INSTALL_NOTES_Windows.md](INSTALL_NOTES_Windows.md)

Installing from source
----------------------

### Required software

- Go >= 1.26 (only needed to build from source -- not required to run a
  pre-built release binary)
- git >= v2

### Steps

1. git clone https://github.com/caltechlibrary/awstools
2. Change directory into the `awstools` directory
3. Make to build, test and install

~~~shell
git clone https://github.com/caltechlibrary/awstools
cd awstools
make
make test
make install
~~~

