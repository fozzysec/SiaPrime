#Installation notes on modified branch oneClientLogFile and removeClientLogging

##Installation
----
###Clone to local path
First clone SiaPrime into the `$GOPATH/src/SiaPrime` to solve local dependency.

```
git clone https://github.com/IntrepidPool/IntrepidPoolStratumSiaPrime.git $GOPATH/src/SiaPrime
```
###Switch to the desired branch
```
git checkout <target branch>
```
###Install dependencies
```
make dependencies
```
**Note: The Makefile is modified to use GitHub mirrors to replace golang.org repos to avoid connection problem in China**
###Build
```
make release
```
