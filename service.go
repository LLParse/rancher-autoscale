package main

import (
  "os"
  "fmt"
  "log"
  "time"
  "strings"
  "net/http"
  "io/ioutil"
  "encoding/json"
  "golang.org/x/net/websocket"
  "github.com/urfave/cli"
  "github.com/google/cadvisor/client"
  "github.com/google/cadvisor/info/v1"
  "github.com/rancher/go-rancher-metadata/metadata"
  rclient "github.com/rancher/go-rancher/client"
)

const (
  metadataUrl = "http://rancher-metadata.rancher.internal/2015-12-19"
)

func ServiceCommand() cli.Command {
  return cli.Command{
    Name:  "service",
    Usage: "Autoscale a service",
    ArgsUsage: "<stack/service>",
    Action: ScaleService,
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
      cli.StringFlag{
        Name:  "url",
        Usage: "Rancher API URL",
        Value: os.Getenv("CATTLE_URL"),
      },
      cli.StringFlag{
        Name:  "access-key",
        Usage: "Rancher Access Key",
        Value: os.Getenv("CATTLE_ACCESS_KEY"),
      },
      cli.StringFlag{
        Name:  "secret-key",
        Usage: "Rancher Secret Key",
        Value: os.Getenv("CATTLE_SECRET_KEY"),
      },
    },
  }
}

type AutoscaleClient struct {
  Stack       string
  Service     string
  MClient     *metadata.Client
  RContainers []metadata.Container
  CContainers []v1.ContainerInfo
}

func NewAutoscaleClient(c *cli.Context, stackservice string) *AutoscaleClient {
  tokens := strings.Split(stackservice, "/")
  stackName := tokens[0]
  serviceName := tokens[1]
  mclient := metadata.NewClient(metadataUrl)

  // get 'rancher' containers
  //rcontainers, err := mclient.GetServiceContainers(service, stack)
  //if err != nil {
//    log.Fatalln(err)
//  }
//  fmt.Println("Rancher Containers:", rcontainers)

  //  curl -s -u $CATTLE_ACCESS_KEY:$CATTLE_SECRET_KEY $CATTLE_URL/projects | jq -r .data[].id
  client, err := rclient.NewRancherClient(&rclient.ClientOpts{
    Url:       c.String("url"),
    AccessKey: c.String("access-key"),
    SecretKey: c.String("secret-key"),
  })
  if err != nil {
    log.Fatalln(err)
  }

  serviceFilter := make(map[string]interface{})
  serviceFilter["name"] = serviceName
  serviceCollection, err := client.Service.List(&rclient.ListOpts{
    Filters: serviceFilter,
  })
  if len(serviceCollection.Data) > 1 {
    log.Fatalln("Service name wasn't unique:", serviceName)
  }
  service := serviceCollection.Data[0]
  statsUrl := fmt.Sprintf("%s/projects/%s/services/%s/containerstats", c.String("url"), service.AccountId, service.Id)
  fmt.Println(statsUrl)
  
  // get stats
  resp, err := http.Get(statsUrl)
  if err != nil {
    log.Fatalln(err)
  }
  defer resp.Body.Close()

  body, err := ioutil.ReadAll(resp.Body)
  if err != nil {
    log.Fatalln(err)
  }

  kv := make(map[string]interface{})
  if err := json.Unmarshal(body, &kv); err != nil {
    log.Fatalln(err)
  }

  wsendpoint := kv["url"].(string) + "?token=" + kv["token"].(string)
  ws, err := websocket.Dial(wsendpoint, "", statsUrl)
  if err != nil {
    log.Fatalln(err)
  }
  defer ws.Close()

  var msg = make([]byte, 65536)
  var n int
  if n, err = ws.Read(msg); err != nil {
      log.Fatal(err)
  }
  fmt.Printf("Received: %s.\n", msg[:n])


  os.Exit(0)



  // get hosts
  var rcontainers []metadata.Container
  hosts, err := mclient.GetContainerHosts(rcontainers)
  if err != nil {
    log.Fatalln(err)
  }
  fmt.Println("Rancher Hosts:", hosts)


  // get 'cadvisor' containers
  ccontainers, err := GetCadvisorContainers(rcontainers, hosts)
  if err != nil {
    log.Fatalln(err)
  }
  fmt.Println("cAdvisor Containers:", ccontainers)

  return &AutoscaleClient{
    Stack: stackName,
    Service: serviceName,
    MClient: mclient,
    RContainers: rcontainers,
    CContainers: ccontainers,
  }
}

func (c *AutoscaleClient) Monitor() error {
  fmt.Printf("Monitoring service '%s' in stack '%s'\n", c.Service, c.Stack)
  return nil
}

func ScaleService(c *cli.Context) error {
  stackservice := c.Args().First()
  if stackservice == "" {
    cli.ShowCommandHelp(c, "service")
    os.Exit(1)
  }

  client := NewAutoscaleClient(c, stackservice)
  return client.Monitor()
}

func GetCadvisorContainers(rancherContainers []metadata.Container, hosts []metadata.Host) (cinfo []v1.ContainerInfo, err error) {
  for _, host := range hosts {
    address := "http://" + host.AgentIP + ":9244/"
    cli, err := client.NewClient(address)
    if err != nil {
      return nil, err
    }

    containers, err := cli.AllDockerContainers(&v1.ContainerInfoRequest{ NumStats: -1 })
    if err != nil {
      return nil, err
    }

    for _, container := range containers {
      for _, rancherContainer := range rancherContainers {
        if rancherContainer.Name == container.Labels["io.rancher.container.name"] {
          cinfo = append(cinfo, container)
          break
        }
      }
    }
  }

  return 
}
