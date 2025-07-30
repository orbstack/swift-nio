import SwiftUI

private let libraryIcons: Set<String> = ["aarch64", "adminer", "adonisjs", "aerospike", "aframe", "aftereffects", "akka", "algolia", "almalinux", "alpine", "alpinejs", "alt", "amazoncorretto", "amazonlinux", "anaconda", "android", "androidstudio", "angular", "angularjs", "angularmaterial", "ansible", "ansys", "antdesign", "apache", "apacheairflow", "apachekafka", "apachespark", "apex", "api-firewall", "apl", "apollographql", "appcelerator", "apple", "appwrite", "arangodb", "archlinux", "arduino", "argocd", "artixlinux", "astro", "atom", "awk", "axios", "azure", "azuredevops", "azuresqldatabase", "babel", "babylonjs", "backbonejs", "backdrop", "ballerina", "bamboo", "bash", "bazel", "beats", "behance", "bevyengine", "biome", "bitbucket", "blazor", "blender", "bonita", "bootstrap", "bower", "browserstack", "buildpack-deps", "bulma", "bun", "busybox", "c", "caddy", "cairo", "cakephp", "canva", "capacitor", "carbon", "cassandra", "centos", "ceylon", "chakraui", "chartjs", "chrome", "chronograf", "circleci", "cirros", "clarity", "clearlinux", "clefos", "clickhouse", "clion", "clojure", "clojurescript", "cloudflare", "cloudflareworkers", "cloudrun", "cmake", "cobol", "codeac", "codecov", "codeigniter", "codepen", "coffeescript", "composer", "confluence", "consul", "contao", "convertigo", "corejs", "cosmosdb", "couchbase", "couchdb", "cpanel", "cplusplus", "crate", "crystal", "csharp", "css3", "cucumber", "cypressio", "d3js", "dart", "datadog", "datagrip", "dataspell", "datatables", "dbeaver", "debian", "delphi", "denojs", "detaspace", "devicon", "digitalocean", "discloud", "discordjs", "django", "djangorest", "docker", "doctrine", "dot-net", "dotnetcore", "dovecot", "dreamweaver", "dropwizard", "drupal", "duckdb", "dyalog", "dynamodb", "dynatrace", "eclipse-mosquitto", "eclipse-temurin", "eclipse", "ecto", "eggdrop", "elasticsearch", "electron", "eleventy", "elixir", "elm", "emacs", "embeddedc", "ember", "emqx", "entityframeworkcore", "envoy", "erlang", "eslint", "expo", "express-gateway", "express", "facebook", "fastapi", "fastify", "faunadb", "feathersjs", "fedora", "fiber", "figma", "filamentphp", "filezilla", "firebase", "firebird", "firefox", "flask", "flink", "fluentd", "flutter", "forgejo", "fortran", "foundation", "framermotion", "framework7", "friendica", "fsharp", "fusion", "gardener", "gatling", "gatsby", "gazebo", "gcc", "gentoo", "geonetwork", "ghost", "gimp", "git", "gitbook", "github", "githubactions", "githubcodespaces", "gitkraken", "gitlab", "gitpod", "gitter", "gleam", "glitch", "go", "godot", "goland", "golang", "google", "googlecloud", "googlecolab", "gradle", "grafana", "grails", "graphql", "groovy", "grpc", "grunt", "gulp", "hadoop", "handlebars", "haproxy", "harbor", "hardhat", "harvester", "haskell", "haxe", "hello-world", "helm", "heroku", "hibernate", "homebrew", "hoppscotch", "html5", "htmx", "httpd", "hugo", "hylang", "hyperv", "ibm-semeru-runtimes", "ie10", "ifttt", "illustrator", "inertiajs", "influxdb", "inkscape", "insomnia", "intellij", "ionic", "irssi", "jaegertracing", "jamstack", "jasmine", "java", "javascript", "jeet", "jekyll", "jenkins", "jest", "jetbrains", "jetpackcompose", "jetty", "jhipster", "jira", "jiraalign", "joomla", "jquery", "jruby", "json", "jule", "julia", "junit", "jupyter", "k3os", "k3s", "k6", "kaggle", "kaldi", "kalilinux", "kapacitor", "karatelabs", "karma", "kdeneon", "keras", "kibana", "knexjs", "kong", "kotlin", "krakend", "krakenjs", "ktor", "kubeflow", "kubernetes", "labview", "laminas", "laravel", "laraveljetstream", "latex", "leetcode", "libgdx", "lightstreamer", "linkedin", "linux", "linuxmint", "liquibase", "livewire", "llvm", "lodash", "logstash", "love2d", "lua", "lumen", "mageia", "magento", "mapbox", "mariadb", "markdown", "materializecss", "materialui", "matlab", "matomo", "matplotlib", "mattermost", "maven", "maya", "mediawiki", "memcached", "mercurial", "meteor", "microsoftsqlserver", "minitab", "mithril", "mobx", "mocha", "modx", "moleculer", "mongo-express", "mongo", "mongodb", "mongoose", "monica", "mono", "monogame", "moodle", "msdos", "mysql", "nano", "nats-streaming", "nats", "neo4j", "neovim", "nestjs", "netbeans", "netbox", "netlify", "networkx", "neurodebian", "newrelic", "nextjs", "nginx", "ngrok", "ngrx", "nhibernate", "nim", "nimble", "nixos", "node", "nodejs", "nodemon", "nodered", "nodewebkit", "nomad", "norg", "notion", "npm", "npss", "nuget", "numpy", "nuxt", "nuxtjs", "oauth", "objectivec", "ocaml", "odoo", "ohmyzsh", "okta", "open-liberty", "openal", "openapi", "opencl", "opencv", "opengl", "openjdk", "openstack", "opensuse", "opentelemetry", "opera", "oracle", "oraclelinux", "orientdb", "ory", "p5js", "packer", "pandas", "passport", "percona", "perl", "pfsense", "phalcon", "phoenix", "photon", "photonengine", "photoshop", "php-zendserver", "php", "phpmyadmin", "phpstorm", "pixijs", "playwright", "plone", "plotly", "pm2", "pnpm", "podman", "poetry", "polygon", "portainer", "postcss", "postfixadmin", "postgres", "postgresql", "postman", "powershell", "premierepro", "primeng", "prisma", "processing", "processwire", "prolog", "prometheus", "protractor", "proxmox", "pug", "pulsar", "pulumi", "puppeteer", "purescript", "putty", "pycharm", "pypi", "pypy", "pytest", "python", "pytorch", "qodana", "qt", "qtest", "quarkus", "quasar", "qwik", "r-base", "r", "rabbitmq", "racket", "radstudio", "rails", "railway", "rakudo-star", "rancher", "raspberrypi", "reach", "react", "reactbootstrap", "reactnative", "reactnavigation", "reactrouter", "readthedocs", "realm", "rect", "redhat", "redis", "redmine", "redux", "reflex", "registry", "remix", "renpy", "replit", "rethinkdb", "rexx", "rider", "rocket.chat", "rocksdb", "rockylinux", "rollup", "ros", "rspec", "rstudio", "ruby", "rubymine", "rust", "rxjs", "safari", "salesforce", "sanity", "sapmachine", "sass", "satosa", "scala", "scalingo", "scikitlearn", "sdl", "selenium", "sema", "sentry", "sequelize", "shopware", "shotgrid", "silverpeas", "sketch", "sl", "slack", "socketio", "solidity", "solidjs", "solr", "sonarqube", "sourceengine", "sourcetree", "spack", "spark", "spicedb", "spring", "spss", "spyder", "sqlalchemy", "sqldeveloper", "sqlite", "ssh", "stackblitz", "stackoverflow", "stenciljs", "storm", "storybook", "streamlit", "styledcomponents", "stylus", "subversion", "sulu", "supabase", "surrealdb", "svelte", "svgo", "swagger", "swift", "swiper", "swipl", "symfony", "tailwindcss", "talos", "tauri", "teamspeak", "telegraf", "teleport", "tensorflow", "terraform", "terramate", "tex", "thealgorithms", "threedsmax", "threejs", "thymeleaf", "titaniumsdk", "tmux", "tomcat", "tomee", "tortoisegit", "towergit", "traefik", "traefikmesh", "traefikproxy", "travis", "trello", "trpc", "turbo", "twilio", "twitter", "typescript", "typo3", "ubuntu", "unifiedmodelinglanguage", "unit", "unity", "unix", "unrealengine", "uwsgi", "v8", "vaadin", "vagrant", "vala", "varnish", "vault", "veevalidate", "vercel", "vertx", "vim", "visualbasic", "visualstudio", "vite", "vitejs", "vitess", "vitest", "vscode", "vscodium", "vsphere", "vuejs", "vuestorefront", "vuetify", "vulkan", "vyper", "waku", "wasm", "web3js", "webflow", "webgpu", "weblate", "webpack", "websphere-liberty", "webstorm", "windows11", "windows8", "wolfram", "woocommerce", "wordpress", "xamarin", "xcode", "xd", "xml", "xwiki", "yaml", "yarn", "yii", "yourls", "yugabytedb", "yunohost", "zend", "zig", "znc", "zookeeper", "zsh", "zustand", ]

private func getImage(for container: DKContainer) -> String? {
    // TODO: smarter resolution based on image hash -> DKImage

    // strip version tag
    guard let image = container.image.split(separator: ":").first else {
        return nil
    }

    // get last two parts of image name:
    let parts = image.split(separator: "/")
    guard parts.count >= 1 else {
        return nil
    }

    let name = String(parts[parts.count - 1])
    let org = parts.count >= 2 ? String(parts[parts.count - 2]) : nil

    // 1. prefer name
    if libraryIcons.contains(name) {
        return "container_img_library/\(name)"
    }

    // 2. try org
    if let org = org, libraryIcons.contains(org) {
        return "container_img_library/\(org)"
    }

    // placeholder
    return nil
}

struct DockerContainerImage: View {
    let container: DKContainer

    @ViewBuilder private var placeholder: some View {
        // 28px
        let color = SystemColors.forString(container.userName)
        Image(systemName: "shippingbox.fill")
            .resizable()
            .aspectRatio(contentMode: .fit)
            .frame(width: 16, height: 16)
            .padding(6)
            .foregroundColor(Color(hex: 0xFAFAFA))
            .background(Circle().fill(color))
            // rasterize so opacity works on it as one big image
            .drawingGroup(opaque: false)
    }

    var body: some View {
        if let image = getImage(for: container) {
            Image(image)
                .resizable()
                .aspectRatio(contentMode: .fit)
                .frame(width: 28, height: 28)
        } else {
            placeholder
        }
    }
}
