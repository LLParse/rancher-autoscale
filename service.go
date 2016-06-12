package main

import (
  "os"
  "fmt"
  "time"
  "strings"
  "github.com/urfave/cli"
  //"github.com/google/cadvisor/client"
  "github.com/rancher/go-rancher-metadata/metadata"
)

const (
  metadataUrl = "http://rancher-metadata.rancher.internal/2015-12-19"
)

func ServiceCommand() cli.Command {
  return cli.Command{
    Name:  "service",
    Usage: "Autoscale a service",
    ArgsUsage: "<stack/service>",
    Action: scaleService,
    Flags: []cli.Flag{
      cli.Float64Flag{
        Name:  "cpu",
        Usage: "CPU Usage threshold in percent",
        Value: 80,
      },
      cli.Float64Flag{
        Name:  "mem",
        Usage: "Memory Usage threshold in percent",
        Value: 80,
      },
      cli.DurationFlag{
        Name:  "period",
        Usage: "",
        Value: 60 * time.Second,
      },
      cli.DurationFlag{
        Name:  "warmup",
        Usage: "",
        Value: 60 * time.Second,
      },
    },
  }
}

func scaleService(c *cli.Context) error {
  stackservice := c.Args().First()
  if stackservice == "" {
    cli.ShowCommandHelp(c, "service")
    os.Exit(1)
  }

  tokens := strings.Split(stackservice, "/")
  stack := tokens[0]
  service := tokens[1]
  fmt.Printf("Monitoring service '%s' in stack '%s'\n", service, stack)

  // get containers
  m := metadata.NewClient(metadataUrl)
  containers, err := m.GetServiceContainers(service, stack)
  if err != nil {
    return err
  }
  fmt.Println(containers)

  hosts, err := m.GetContainerHosts(containers)
  if err != nil {
    return err
  }

  // collect metrics from each host
  for _, host := range hosts {
    fmt.Println(host)
  }

  return nil
}
