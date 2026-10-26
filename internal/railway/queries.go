package railway

// The queries below are the whole read surface against Railway's public API.
// They are kept as constants so a schema change is one file, and so the
// recorded fixtures and these documents can be compared by eye.

// queryProjects lists the projects a token can see, for resolving a name.
const queryProjects = `query Projects {
  projects(first: 100) {
    edges { node { id name } }
  }
}`

// queryWorkspaces lists the workspaces the login belongs to. A workspace or
// account login is not authorized for the top-level projects query, but it can
// list each workspace's projects.
const queryWorkspaces = `query Workspaces {
  me { workspaces { id name } }
}`

// queryWorkspaceProjects lists one workspace's projects, for resolving a name.
const queryWorkspaceProjects = `query WorkspaceProjects($id: String!) {
  workspace(workspaceId: $id) {
    projects { edges { node { id name } } }
  }
}`

// queryProject reads one project and its environments.
const queryProject = `query Project($id: String!) {
  project(id: $id) {
    id
    name
    description
    environments { edges { node { id name } } }
  }
}`

// queryEnvironment reads every service instance and volume in one environment.
// source tells a git service from an image one; the commands and settings are
// what a service needs to build and run the same way. A setting can also live in
// the repository's railway.json or railway.toml (resolvedFileConfig), or only in
// what the latest deployment ran (latestDeployment.meta.serviceManifest, which
// also holds the region and replicas of a multi-region service).
const queryEnvironment = `query Environment($id: String!) {
  environment(id: $id) {
    id
    name
    serviceInstances { edges { node {
      serviceId
      serviceName
      source { repo image }
      buildCommand
      startCommand
      rootDirectory
      dockerfilePath
      healthcheckPath
      healthcheckTimeout
      restartPolicyType
      restartPolicyMaxRetries
      numReplicas
      region
      cronSchedule
      preDeployCommand
      railwayConfigFile
      resolvedFileConfig { configFile fileManifest }
      latestDeployment { id status meta }
    } } }
    volumeInstances { edges { node {
      id
      mountPath
      sizeMB
      serviceId
      volume { id name }
    } } }
  }
}`

// queryVariables reads a service's variables unrendered, so a reference such as
// ${{Postgres.DATABASE_URL}} stays a reference instead of a password-bearing URL.
// Without serviceId it reads the environment's shared variables.
const queryVariables = `query Variables($projectId: String!, $environmentId: String!, $serviceId: String) {
  variables(projectId: $projectId, environmentId: $environmentId, serviceId: $serviceId, unrendered: true)
}`

// queryDomains reads a service's domains, keeping Railway's own subdomain apart
// from a customer's custom domain.
const queryDomainsFull = `query Domains($projectId: String!, $environmentId: String!, $serviceId: String!) {
  domains(projectId: $projectId, environmentId: $environmentId, serviceId: $serviceId) {
    serviceDomains { id domain suffix targetPort }
    customDomains { id domain targetPort }
  }
}`

// queryTriggers reads the branch a git service deploys from.
const queryTriggers = `query Triggers($projectId: String!, $environmentId: String!, $serviceId: String!) {
  deploymentTriggers(projectId: $projectId, environmentId: $environmentId, serviceId: $serviceId) {
    edges { node { id branch repository } }
  }
}`

// queryPlugins reads the legacy plugin list. Railway deprecated Project.plugins
// ("Plugins have been removed"), so a project created under the current model
// answers null or an error; the caller then classifies services instead.
const queryPlugins = `query Plugins($id: String!) {
  project(id: $id) {
    plugins { edges { node { id name friendlyName } } }
  }
}`
