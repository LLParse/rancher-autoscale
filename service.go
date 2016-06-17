package main

import (
  "os"
  "fmt"
  "log"
  "time"
  "strings"
  "github.com/urfave/cli"
  "github.com/google/cadvisor/client"
  "github.com/google/cadvisor/info/v1"
  rclient "github.com/rancher/go-rancher/client"
  "github.com/rancher/go-rancher-metadata/metadata"
)

const (
  // Rancher metadata endpoint URL 
  metadataUrl = "http://rancher-metadata.rancher.internal/2015-12-19"
  // interval at which each goroutine polls cAdvisor for metrics
  pollCadvisorInterval = 2 * time.Second
  // interval at which to poll metadata
  pollMetadataInterval = 10 * time.Second
  // interval at which to print statistics
  printStatisticsInterval = 10 * time.Second
  // interval at which to analyze metrics, should be >= pollCadvisorInterval
  analyzeMetricsInterval = 2 * time.Second
)

func ServiceCommand() cli.Command {
  return cli.Command{
    Name:  "service",
    Usage: "Autoscale a service",
    ArgsUsage: "<stack/service>",
    Action: ScaleService,
    Flags: []cli.Flag{
      cli.Float64Flag{
        Name:  "min-cpu",
        Usage: "Minimum CPU usage threshold in percent",
        Value: 0,
      },
      cli.Float64Flag{
        Name:  "max-cpu",
        Usage: "Maximum CPU usage threshold in percent",
        Value: 100,
      },
      cli.Float64Flag{
        Name:  "min-mem",
        Usage: "Minimum Memory usage threshold in MiB",
        Value: 0,
      },
      cli.Float64Flag{
        Name:  "max-mem",
        Usage: "Memory Usage threshold in percent",
        Value: 4096,
      },
      cli.StringFlag{
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
      cli.StringFlag{
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

type AutoscaleContext struct {
  // configuration argument
  StackName       string
  Service         metadata.Service

  RClient         *rclient.RancherClient
  RService        *rclient.Service
  
  // configuration parameters
  MinCpuThreshold float64
  MaxCpuThreshold float64
  MinMemThreshold float64
  MaxMemThreshold float64
  And             bool
  Period          time.Duration
  Warmup          time.Duration
  Cooldown        time.Duration
  Verbose         bool

  mClient         *metadata.Client
  mContainers     []metadata.Container
  mHosts          []metadata.Host
  CContainers     []v1.ContainerInfo
  cInfoMap        map[string]*v1.ContainerInfo
  requestCount    int
  addedCount      int
  deletedCount    int

  done            chan bool
}

func NewAutoscaleContext(c *cli.Context) *AutoscaleContext {
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

  rcontainers, err := mclient.GetServiceContainers(serviceName, stackName)
  if err != nil {
    log.Fatalln(err)
  }

  // get rancher hosts
  rhosts, err := mclient.GetContainerHosts(rcontainers)
  if err != nil {
    log.Fatalln(err)
  }

  rcli, err := rclient.NewRancherClient(&rclient.ClientOpts{
    Url:       c.String("url"),
    AccessKey: c.String("access-key"),
    SecretKey: c.String("secret-key"),
  })
  if err != nil {
    log.Fatalln(err)
  }

  services, err := rcli.Service.List(&rclient.ListOpts{
    Filters: map[string]interface{}{
      "uuid": service.UUID,
    },
  })
  if err != nil {
    log.Fatalln(err)
  }
  if len(services.Data) > 1 {
    log.Fatalln("Multiple services returned with UUID", service.UUID)
  }

  client := &AutoscaleContext{
    StackName: stackName,
    Service: service,
    RClient: rcli,
    RService: &services.Data[0],
    MinCpuThreshold: c.Float64("min-cpu"),
    MaxCpuThreshold: c.Float64("max-cpu"),
    MinMemThreshold: c.Float64("min-mem"),
    MaxMemThreshold: c.Float64("max-mem"),
    And: c.String("and") == "true",
    Period: c.Duration("period"),
    Warmup: c.Duration("warmup"),
    Cooldown: c.Duration("cooldown"),
    Verbose: c.String("verbose") == "true",
    mClient: mclient,
    mContainers: rcontainers,
    mHosts: rhosts,
    cInfoMap: make(map[string]*v1.ContainerInfo),
    done: make(chan bool),
  }

  fmt.Printf("Monitoring '%s' service in '%s' stack, %d containers across %d hosts\n", 
    serviceName, stackName, len(rcontainers), len(rhosts))
  if client.Verbose {
    fmt.Println("Container Information:")
    for _, container := range rcontainers {
      fmt.Printf("\t(%s) %v\n", container.Name, container)
    }
    fmt.Println("Host Information:")
    for _, host := range rhosts {
      fmt.Printf("\t(%s) %v\n", host.Name, host)
    }
  }

  // get cadvisor containers

  return client
}

func ScaleService(c *cli.Context) error {
  ctx := NewAutoscaleContext(c)
  return ctx.GetCadvisorContainers()
}

func (c *AutoscaleContext) GetCadvisorContainers() error {
  var cinfo []v1.ContainerInfo

  metrics := make(chan v1.ContainerInfo)
  defer close(metrics)

  for _, host := range c.mHosts {
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
      for _, rancherContainer := range c.mContainers {
        if rancherContainer.Name == container.Labels["io.rancher.container.name"] {
          cinfo = append(cinfo, container)
          go c.PollContinuously(container.Id, host.AgentIP, metrics)

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
  go c.ProcessMetrics(metrics)
  c.PollMetadataChanges()

  return nil
}

// indefinitely poll for service scale changes
func (c *AutoscaleContext) PollMetadataChanges() {
  for {
    time.Sleep(pollMetadataInterval)

    service, err := c.mClient.GetServiceByName(c.StackName, c.Service.Name)
    if err != nil {
      log.Println(err)
    }

    // if the service is scaled up/down, we accomplished our goal
    if service.Scale != c.Service.Scale {
      select {
      case <-c.done:
        // already shutting down, we caused the scale change
      default:
        fmt.Printf("Detected scale up: %d -> %d\n", c.Service.Scale, service.Scale)
      }
      c.done <- true
      fmt.Printf("Exiting")
      break
    }
  }
}

// process incoming metrics
func (c *AutoscaleContext) ProcessMetrics(metrics <-chan v1.ContainerInfo) {
  fmt.Println("Started processing metrics")
  for {
    select {
    case <-c.done:
      c.done <- true
      fmt.Println("Stopped processing metrics")
      return
    case metric := <-metrics:
      if _, exists := c.cInfoMap[metric.Id]; !exists {
        c.cInfoMap[metric.Id] = &metric
      } else {
        // append new metrics
        c.addedCount += len(metric.Stats)
        c.cInfoMap[metric.Id].Stats = append(c.cInfoMap[metric.Id].Stats, metric.Stats...)

        if len(c.cInfoMap[metric.Id].Stats) >= 2 {
          c.DeleteOldMetrics(c.cInfoMap[metric.Id])
          c.AnalyzeMetrics()
        }
      }
      c.PrintStatistics()
    }
  }
}

func (c *AutoscaleContext) PrintStatistics() {
  if c.requestCount % (int(printStatisticsInterval / pollCadvisorInterval) * c.Service.Scale) == 0 {
    fmt.Printf("added: %d, deleted: %d, in-memory: %d, requests: %d\n", 
      c.addedCount, c.deletedCount, c.addedCount - c.deletedCount, c.requestCount)

    if c.Verbose {
      for _, info := range c.cInfoMap {
        metrics := len(info.Stats)
        window := StatsWindow(info.Stats, 0, 10 * time.Millisecond)

        fmt.Printf("\t(%s) metrics: %d, window: %v, rate: %f/sec\n", info.Labels["io.rancher.container.name"], 
          metrics, window, float64(metrics) / float64(window / time.Second))
      }      
    }
  }
}

// analyze metric window and trigger scale operations
func (c *AutoscaleContext) AnalyzeMetrics() {
  if c.requestCount % (int(analyzeMetricsInterval / pollCadvisorInterval) * c.Service.Scale) != 0 {
    return
  }

  // average cumulative CPU usage (over configured period)
  averageCpu := float64(0)
  // average cumulative RAM usage (instantaneous) maybe should be avg over time
  averageMem := float64(0)

  for _, cinfo := range c.cInfoMap {
    stats := cinfo.Stats

    // we absolutely need two or more metrics to look at a time window
    if len(stats) < 2 {
      return
    }

    begin := stats[0]
    end := stats[len(stats)-1]
    duration := end.Timestamp.Sub(begin.Timestamp)

    // we absolutely need a full time window to make decisions
    if duration < c.Period {
      return
    }

    averageCpu += float64(end.Cpu.Usage.Total - begin.Cpu.Usage.Total) / float64(duration) * 100

    // FIXME (llparse) this needs to be an average
    averageMem += float64(end.Memory.Usage)
  }

  averageCpu /= float64(c.Service.Scale)
  averageMem = averageMem / float64(c.Service.Scale) / 1024 / 1024

  fmt.Printf("avg cpu: %5.1f%%, avg mem: %7.1fMiB\n", averageCpu, averageMem)

  // all conditions must be met
  if c.And {
    if averageCpu >= c.MaxCpuThreshold && averageMem >= c.MaxMemThreshold {
      c.ScaleUp()
    }
    if averageCpu <= c.MinCpuThreshold && averageMem <= c.MinMemThreshold {
      c.ScaleDown()
    }    
  // any condition must be met
  } else {
    if averageCpu >= c.MaxCpuThreshold || averageMem >= c.MaxMemThreshold {
      c.ScaleUp()
    }
    if averageCpu <= c.MinCpuThreshold || averageMem <= c.MinMemThreshold {
      c.ScaleDown()
    }
  }
}

func (c *AutoscaleContext) ScaleUp() {
  c.Scale(1)
}

func (c *AutoscaleContext) ScaleDown() {
  c.Scale(-1)
}

func (c *AutoscaleContext) Scale(offset int64) {
  var adjective string
  var delay time.Duration

  if offset > 0 {
    adjective = "up"
    delay = c.Warmup
  } else {
    adjective = "down"
    delay = c.Cooldown
  }

  newScale := c.RService.Scale + offset

  fmt.Printf("Triggered scale %s: %d -> %d\n", adjective, c.RService.Scale, newScale)

  // sometimes Rancher takes ages to respond so do this async
  go func() {
    _, err := c.RClient.Service.Update(c.RService, map[string]interface{}{
      "scale": newScale,
    })
    if err != nil {
      log.Fatalln(err)
    }
  }()

  // process completes when we scale
  c.done <- true

  // warmup or cooldown
  if offset < 0 {
    fmt.Printf("Cooling down for %v\n", delay)
  } else {
    fmt.Printf("Warming up for %v\n", delay)
  }
  time.Sleep(delay)

  fmt.Println("Exiting")
}


// delete metrics outside of the time window
func (c *AutoscaleContext) DeleteOldMetrics(cinfo *v1.ContainerInfo) {
  precision := 100 * time.Millisecond
  for ; StatsWindow(cinfo.Stats, 1, precision) >= c.Period; c.deletedCount += 1 {
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
func (c *AutoscaleContext) PollContinuously(containerId string, hostIp string, metrics chan<- v1.ContainerInfo) {
  address := "http://" + hostIp + ":9244/"
  cli, err := client.NewClient(address)
  if err != nil {
    log.Fatalln(err)
  }

  start := time.Now()
  for {
    select {
    case <-c.done:
      c.done <- true
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
