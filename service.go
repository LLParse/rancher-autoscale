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

  if t, ok := kv["type"].(string); ok && t == "error" {
    log.Fatalln("Error:", kv["message"].(string))
  }

  metric := make(chan []byte)
  fmt.Println("kv:")
  for k, v := range kv {
    fmt.Printf("%s: %s\n", k, v)
  }
  wsendpoint := kv["url"].(string) + "?token=" + kv["token"].(string)

  go ReadMetrics(wsendpoint, statsUrl, metric)

  for {
    select {
    case m := <-metric:
      fmt.Println(string(m))
    }
  }

  os.Exit(0)

  return &AutoscaleClient{
    Stack: stackName,
    Service: serviceName,
    MClient: mclient,
  }
}

func ReadMetrics(url string, origin string, metric chan<- []byte) error {
  ws, err := websocket.Dial(url, "", origin)
  if err != nil {
    return err
  }
  defer ws.Close()

  var msg = make([]byte, 65536)
  var n int
  for {
    for {
      if n, err = ws.Read(msg); err != nil {
          return err
      }
      metric <- msg[:n]
    }
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
