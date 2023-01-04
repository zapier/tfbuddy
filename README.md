# TFBuddy

First, you need to get the repo added to helm!

```sh
helm repo add tfbuddy https://zapier.github.io/tfbuddy/
helm repo update
helm search repo tfbuddy -l
```

Then you can install it:

```sh
helm install tfbuddy tfbuddy/tfbuddy 0.2.0
```
