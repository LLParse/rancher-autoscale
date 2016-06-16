package main

import (
  "os"
  "fmt"
  "log"
  "time"
  "strings"
  //"net/http"
  //"io/ioutil"
  //"encoding/json"
  //"golang.org/x/net/websocket"
  "github.com/urfave/cli"
  "github.com/google/cadvisor/client"
  "github.com/google/cadvisor/info/v1"
  "github.com/rancher/go-rancher-metadata/metadata"
  //rclient "github.com/rancher/go-rancher/client"
)

const (
  // Rancher metadata endpoint URL 
  metadataUrl = "http://rancher-metadata.rancher.internal/2015-12-19"
  // interval at which to poll cAdvisor for metrics
  pollCadvisorInterval = 1 * time.Second
  // interval at which to poll metadata for scale changes
  pollMetadataInterval = 15 * time.Second
)

func ServiceCommand() cli.Command {
  return cli.Command{
    Name:  "service",
    Usage: "Autoscale a service",
    ArgsUsage: "<stack/service>",
    Action: ScaleService,
    Flags: []cli.Flag{
      cli.Float64Flag{
        Name:  "cpumin",
        Usage: "Minimum CPU usage threshold in percent",
        Value: 0,
      },
      cli.Float64Flag{
        Name:  "cpumax",
        Usage: "Maximum CPU usage threshold in percent",
        Value: 100,
      },
      cli.Float64Flag{
        Name:  "memmin",
        Usage: "Minimum Memory usage threshold in MiB",
        Value: 0,
      },
      cli.Float64Flag{
        Name:  "memmax",
        Usage: "Memory Usage threshold in percent",
        Value: 4096,
      },
      cli.BoolFlag{
        Name:  "and",
        Usage: "Both CPU and Memory minimum or maximum thresholds must be met",
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
      cli.DurationFlag{
        Name:  "cooldown",
        Usage: "",
        Value: 60 * time.Second,
      },
      cli.BoolFlag{
        Name:  "verbose, v",
        Usage: "Enable verbose logging output",
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
  // configuration argument
  StackName       string
  Service         metadata.Service
  
  // configuration parameters
  MinCpuThreshold float64
  MaxCpuThreshold float64
  MinMemThreshold float64
  MaxMemThreshold float64
  And             bool
  Warmup          time.Duration
  Period          time.Duration
  Verbose         bool

  mClient         *metadata.Client
  mContainers     []metadata.Container
  mHosts          []metadata.Host
  CContainers     []v1.ContainerInfo
  ContainerHosts  map[string]metadata.Host
  cInfoMap        map[string]*v1.ContainerInfo
  requestCount    int
  deleteCount     int
}

func NewAutoscaleClient(c *cli.Context) *AutoscaleClient {
  stackservice := c.Args().First()
  if stackservice == "" {
    cli.ShowCommandHelp(c, "service")
    os.Exit(1)
  }

  tokens := strings.Split(stackservice, "/")
  stackName := tokens[0]
  serviceName := tokens[1]

  mclient := metadata.NewClient(metadataUrl)
  
  service, err := mclient.GetServiceByName(stackName, serviceName)
  if err != nil {
    log.Fatalln(err)
  }
  fmt.Println("Service:", service.Name)

  rcontainers, err := mclient.GetServiceContainers(serviceName, stackName)
  if err != nil {
    log.Fatalln(err)
  }
  fmt.Println("Containers:")
  for _, container := range rcontainers {
    fmt.Println(" ", container.Name)
  }

  // get rancher hosts
  rhosts, err := mclient.GetContainerHosts(rcontainers)
  if err != nil {
    log.Fatalln(err)
  }
  fmt.Println("Rancher Hosts:")
  for _, host := range rhosts {
    fmt.Println(" ", host.Name)
  }

  client := &AutoscaleClient{
    StackName: stackName,
    Service: service,
    MinCpuThreshold: c.Float64("mincpu"),
    MaxCpuThreshold: c.Float64("maxcpu"),
    MinMemThreshold: c.Float64("minmem"),
    MaxMemThreshold: c.Float64("maxmem"),
    And: c.Bool("and"),
    Warmup: c.Duration("warmup"),
    Period: c.Duration("period"),
    Verbose: c.Bool("verbose"),
    mClient: mclient,
    mContainers: rcontainers,
    mHosts: rhosts,
    cInfoMap: make(map[string]*v1.ContainerInfo),
  }

  // get cadvisor containers
  if err := client.GetCadvisorContainers(rcontainers, rhosts); err != nil {
    log.Fatalln(err)
  }

  return client
}

func ScaleService(c *cli.Context) error {
  NewAutoscaleClient(c)
  return nil
}

func (c *AutoscaleClient) GetCadvisorContainers(rancherContainers []metadata.Container, hosts []metadata.Host) error {
  c.ContainerHosts = make(map[string]metadata.Host)
  var cinfo []v1.ContainerInfo

  metrics := make(chan v1.ContainerInfo)
  done := make(chan bool)
  defer close(metrics)
  defer close(done)

  for _, host := range hosts {
    address := "http://" + host.AgentIP + ":9244/"
    cli, err := client.NewClient(address)
    if err != nil {
      return err
    }

    containers, err := cli.AllDockerContainers(&v1.ContainerInfoRequest{ NumStats: 0 })
    if err != nil {
      return err
    }

    for _, container := range containers {
      for _, rancherContainer := range rancherContainers {
        if rancherContainer.Name == container.Labels["io.rancher.container.name"] {
          cinfo = append(cinfo, container)
          c.ContainerHosts[container.Id] = host
          go c.PollContinuously(container.Id, host.AgentIP, metrics, done)

          // spread out the requests evenly
          time.Sleep(time.Duration(int(pollCadvisorInterval) / c.Service.Scale))
          break
        }
      }
    }
  }

  fmt.Println("cAdvisor Containers:")
  for _, container := range cinfo {
    fmt.Println(" ", container.Name)
  }
  c.CContainers = cinfo

  fmt.Printf("Monitoring service '%s' in stack '%s'\n", c.Service.Name, c.StackName)
  go c.ProcessMetrics(metrics, done)

  return c.PollService(done)
}

// indefinitely poll for service scale changes
func (c *AutoscaleClient) PollService(done chan<- bool) error {
  for {
    time.Sleep(pollMetadataInterval)

    service, err := c.mClient.GetServiceByName(c.StackName, c.Service.Name)
    if err != nil {
      return err
    }

    // if the service is scaled up/down, we accomplished our goal
    if service.Scale > c.Service.Scale {
      fmt.Printf("Detected scale up: %d -> %d\n", c.Service.Scale, service.Scale)
      done<-true
      fmt.Printf("Waiting %v for container to warm up.\n", c.Warmup)
      time.Sleep(c.Warmup)
      fmt.Println("Exiting")
      return nil

    } else if service.Scale < c.Service.Scale {
      fmt.Printf("Detected scale down: %d -> %d\n", c.Service.Scale, service.Scale)
      done<-true
      // maybe we need to cool down for certain types of software?
      fmt.Println("Exiting")
      return nil
    }
  }
  return nil
}

// process incoming metrics
func (c *AutoscaleClient) ProcessMetrics(metrics <-chan v1.ContainerInfo, done chan bool) {
  fmt.Println("Started processing metrics")
  for {
    select {
    case metric := <-metrics:
      if _, exists := c.cInfoMap[metric.Id]; !exists {
        c.cInfoMap[metric.Id] = &metric
      } else {
        // append new metrics
        c.cInfoMap[metric.Id].Stats = append(c.cInfoMap[metric.Id].Stats, metric.Stats...)

        if len(c.cInfoMap[metric.Id].Stats) >= 2 {
          // delete old metrics
          c.DeleteOldMetrics(c.cInfoMap[metric.Id])

          // analyze metrics
          c.AnalyzeMetrics(c.cInfoMap[metric.Id])

        }
      }
      // print statistics every 10 seconds
      if c.requestCount % (10 * c.Service.Scale) == 0 {
        c.PrintStatistics()
      }
    case <-done:
      done<-true
      fmt.Println("Stopped processing metrics")
      return
    }
  }
}

func (c *AutoscaleClient) PrintStatistics() {
  fmt.Printf("%d requests, %d deleted\n", c.requestCount, c.deleteCount)
  for _, info := range c.cInfoMap {
    fmt.Printf("  %s: %d metrics, %v window\n", info.Labels["io.rancher.container.name"], 
      len(info.Stats), StatsWindow(info.Stats, 0, 100 * time.Millisecond))
    /*for _, stat := range info.Stats {
      fmt.Printf("  %v\n", stat)
    }*/
  }  
}

// analyze metric window and trigger scale operations
func (c *AutoscaleClient) AnalyzeMetrics(cinfo *v1.ContainerInfo) {
  stats := cinfo.Stats
  begin := stats[0]
  end := stats[len(stats)-1]
  duration := end.Timestamp.Sub(begin.Timestamp)

  // don't compute anything until we have enough metrics
  if duration < c.Period {
    return
  }

  // compute average CPU over entire window
  containerAverageCpu := float64(end.Cpu.Usage.Total - begin.Cpu.Usage.Total) / float64(duration) * 100

  // compute

  fmt.Printf("avg cpu: %v%%, start mem: %v, stop mem: %v\n", containerAverageCpu, begin.Memory.Usage, end.Memory.Usage)

  // make decisions
  if containerAverageCpu >= c.MaxCpuThreshold {
    fmt.Println("*** Trigger scale up")
  }
}

// delete metrics outside of the time window
func (c *AutoscaleClient) DeleteOldMetrics(cinfo *v1.ContainerInfo) {
  precision := 100 * time.Millisecond
  for ; StatsWindow(cinfo.Stats, 1, precision) >= c.Period; c.deleteCount += 1 {
    //if !cinfo.Stats[0].Timestamp.Before(windowStart) || window > 0 && window < c.Period {
    // fmt.Printf("  Deleting %v from %s\n", cinfo.Stats[0].Timestamp, cinfo.Labels["io.rancher.container.name"])
    cinfo.Stats = append(cinfo.Stats[:0], cinfo.Stats[1:]...)
  }
}

func StatsWindow(stats []*v1.ContainerStats, offset int, round time.Duration) time.Duration {
  if len(stats) < 2 {
    return time.Duration(0)
  }
  return stats[len(stats)-1].Timestamp.Round(round).Sub(stats[offset].Timestamp.Round(round))
}

// poll cAdvisor continuously for container metrics
func (c *AutoscaleClient) PollContinuously(containerId string, hostIp string, metrics chan<- v1.ContainerInfo, done chan bool) {
  address := "http://" + hostIp + ":9244/"
  cli, err := client.NewClient(address)
  if err != nil {
    log.Fatalln(err)
  }

  start := time.Now()
  for {
    select {
    case <-done:
      done<-true
      fmt.Printf("Stopped collecting metrics for container %s", containerId)
      return
    default:
    }
    time.Sleep(pollCadvisorInterval)

    newStart := time.Now()
    info, err := cli.DockerContainer(containerId, &v1.ContainerInfoRequest{
      Start: start,
    })
    if err != nil {
      fmt.Println(err)
    }

    start = newStart
    metrics <- info
    c.requestCount += 1
  }
}


  //  curl -s -u $CATTLE_ACCESS_KEY:$CATTLE_SECRET_KEY $CATTLE_URL/projects | jq -r .data[].id
  /*client, err := rclient.NewRancherClient(&rclient.ClientOpts{
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


  os.Exit(0)*/
