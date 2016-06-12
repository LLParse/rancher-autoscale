package main

import (
  "os"
  "github.com/urfave/cli"
)

func main() {
  app := cli.NewApp()
  app.Version = "0.0.1"
  app.Name = "autoscale"
  app.Usage = "Scale Rancher components based on resource usage"
  
  app.Commands = []cli.Command{
    LoadCommand(),
    ServiceCommand(),
  }

  app.Run(os.Args)
}