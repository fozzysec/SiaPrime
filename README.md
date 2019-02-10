# [![SiaPrime Logo](https://siaprime.net/imagestore/primelogo_cb_256x256.png)](http://siaprime.net) v1.3.8

[![Build Status](https://gitlab.com/SiaPrime/SiaPrime/badges/master/build.svg)](https://gitlab.com/SiaPrime/SiaPrime/commits/master)
[![Coverage Report](https://gitlab.com/SiaPrime/SiaPrime/badges/master/coverage.svg)](https://gitlab.com/SiaPrime/SiaPrime/commits/master)
[![GoDoc](https://godoc.org/gitlab.com/SiaPrime/SiaPrime?status.svg)](https://godoc.org/gitlab.com/SiaPrime/SiaPrime)
[![Go Report Card](https://goreportcard.com/badge/gitlab.com/SiaPrime/SiaPrime)](https://goreportcard.com/report/gitlab.com/SiaPrime/SiaPrime)
[![License MIT](https://img.shields.io/badge/License-MIT-brightgreen.svg)](https://img.shields.io/badge/License-MIT-brightgreen.svg)

SiaPrime is a decentralized cloud storage platform based on the Sia core 
protocol. Leveraging smart contracts, client-side encryption, and sophisticated
redundancy (via Reed-Solomon codes), SiaPrime allows users to safely store their 
data with hosts that they do not know or trust. The result is a cloud storage 
marketplace where hosts compete to offer the best service at the lowest price. 
Hosts can include everyone from individual hobbyists up to enterprise-scale 
datacenters with excess capacity to sell. 

The SiaPrime protocol currently provides very basic storage capability. We expect
the SiaPrime product suite will begin to roll out in 2019 as we work in parallel
with SiaPrime core developers to deliver live products as soon as the protocol can 
support them. As they build key features into the network, we'll extend
into an easy-to-use application suite with versions geared at different
customer profiles and market verticals.

![UI](https://gitlab.com/SiaPrime/SiaPrime/raw/master/doc/assets/prime_wallet.png)

Traditional cloud storage is dominated by a small number of companies, with
most based in Silicon Valley. China is also dominated by a few entities
creating region specific barriers and firewalls. The individual companies
decide what level of encryption to offer to users, usually based on the price
each customer is willing to pay. All of the companies are compliant with
national laws and even sometimes, with assisting governmental entities to
restrict or compromise user data. They maintain strict control over customer
data in ways that can jeopardize customer security and privacy.

We are rapidly seeing a public desire to retake control of private data.
Users are demanding companies explain their policies in data gathering and use.
SiaPrime is an important initiative to provide companies, organizations and 
individuals a comprehensive cloud storage environment uncompromised by inside 
or outside interference. This is achieved through fragmenting user data and 
spreading it across a distributed network of hosts. No single datacenter has 
access to your complete data.

These fragments are duplicated over enough hosts that even if several hosts 
disappear, your data is still accessible. Switching to different hosts for 
better geographic coverage, price, speed or reliability is easy as hosts 
compete with each other to provide the best service offering. 

As we develop and release our product suite for customers, SiaPrime will 
offer a complete cloud storage solution for file backup, long term archival 
needs, CDN use cases as well as bridging the gap between online and offline 
strategies. 

The SiaPrime network allows for highly parallel data transfer, which when 
coupled with geographic targeting can significantly reduce latency and access 
to your data no matter if you are storing family photos or large database 
backups.


The process of choosing hosts on the SiaPrime network allows customers to 
choose based on latency, lowest price, widest geographic coverage, or even a 
strict whitelist of IP addresses or public keys. We expect to provide customers 
multiple ways to purchase storage on the SiaPrime network, including simple 
Dropbox-like web interfaces up to sophisticated client dashboards for 
enterprise clients.

Purchasing storage on the network will have several variants, though all 
ultimately use our core cryptocurrency utility token called a PrimeToken. 
PrimeTokens will be available on cryptocurrency exchanges. We will also be 
creating fiat to storage gateways to encourage broad adoption of the SiaPrime 
storage model. 

Because SiaPrime is based on the core SiaPrime protocol, we expect SiaPrime software 
and custom integrations to  facilitate purchase of storage on the main SiaPrime 
network as well as SiaPrime project forks that continue to use the core protocol. As 
the protocol becomes widely used, it is likely individual SiaPrime blockchains will 
experience typical scaling issues. Though we expect the Nebulous team to work 
on solutions, multiple chains/networks have the potential to ease scaling 
issues while providing another layer of redundancy and privacy. 


To get started with SiaPrime, check out the initial SiaPrime guides below. The 
interface is the same except for our branding. As we launch our own product 
suite, the core application should still exist but be less useful:

Check out the guides below:

- [How to Store Data on SiaPrime](https://blog.sia.tech/getting-started-with-private-decentralized-cloud-storage-c9565dc8c854)
- [How to Become a SiaPrime Host](https://blog.sia.tech/how-to-run-a-host-on-sia-2159ebc4725)
- [Using the SiaPrime API](https://blog.sia.tech/api-quickstart-guide-f1d160c05235)


Usage
-----
This release comes with 2 binaries, `spd` and `spc`. `spd` is a background
service, or "daemon," that runs the SiaPrime protocol and exposes an HTTP API on
port 4280. `spc` is a command-line client that can be used to interact with
`spd` in a user-friendly way. There is also a graphical client, [SiaPrime-UI](https://gitlab.com/SiaPrime/SiaPrime-UI), 
which is the preferred way of using SiaPrime for most users. For interested 
developers, the `spd` API is documented [here](doc/API.md).

On Windows, double-click `spd.exe`. For command line operation, navigate to the
containing folder and click File->Open command prompt. Start the `spd` service 
by entering `spd` and pressing Enter. The command prompt may appear to freeze; 
this means `spd` is waiting for requests. Windows users may see a warning from 
Windows Firewall; be sure to check both boxes ("Private networks" and "Public 
networks") and click "Allow access." You can now run `spc` (in a separate command
prompt) or SiaPrime-UI to interact with `spd`. 

Building From Source
--------------------

To build from source, [Go 1.10 must be installed](https://golang.org/doc/install)
on the system. Make sure your `$GOPATH` is set, and then simply use `go get`:

```
go get -u gitlab.com/SiaPrime/SiaPrime/...
```

This will download the SiaPrime repo to your `$GOPATH/src` folder and install 
the`spd` and `spc` binaries in your `$GOPATH/bin` folder.

To stay up-to-date, run the previous `go get` command again. Alternatively, you
can use the Makefile provided in this repo. Run `git pull origin master` to
pull the latest changes, and `make release` to build the new binaries. You
can also run `make test` and `make test-long` to run the short and full test
suites, respectively. Finally, `make cover` will generate code coverage reports
for each package; they are stored in the `cover` folder and can be viewed in
your browser.
