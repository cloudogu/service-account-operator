#!groovy

@Library('github.com/cloudogu/ces-build-lib@5.1.0')
import com.cloudogu.ces.cesbuildlib.*

git = new Git(this, "cesmarvin")
git.committerName = 'cesmarvin'
git.committerEmail = 'cesmarvin@cloudogu.com'
gitflow = new GitFlow(this, git)
github = new GitHub(this, git)
changelog = new Changelog(this)
Docker docker = new Docker(this)
gpg = new Gpg(this, docker)
goVersion = "1.26.2"
Makefile makefile = new Makefile(this)

repositoryOwner = "cloudogu"
repositoryName = "service-account-operator"
project = "github.com/${repositoryOwner}/${repositoryName}"
registry = "registry.cloudogu.com"
registryNamespace = "k8s"
helmTargetDir = "target/k8s"
helmChartDir = "${helmTargetDir}/helm"

productionReleaseBranch = "main"
developmentBranch = "develop"
currentBranch = "${env.BRANCH_NAME}"

node('docker') {
    timestamps {
        properties([
                buildDiscarder(logRotator(numToKeepStr: '10')),
                disableConcurrentBuilds(),
                parameters([
                        choice(name: 'TrivySeverityLevels', choices: [TrivySeverityLevel.CRITICAL, TrivySeverityLevel.HIGH_AND_ABOVE, TrivySeverityLevel.MEDIUM_AND_ABOVE, TrivySeverityLevel.ALL], description: 'The levels to scan with trivy'),
                        choice(name: 'TrivyStrategy', choices: [TrivyScanStrategy.UNSTABLE, TrivyScanStrategy.FAIL, TrivyScanStrategy.IGNORE], description: 'Define whether the build should be unstable, fail or whether the error should be ignored if any vulnerability was found.'),
                ])
        ])

        stage('Checkout') {
            checkout scm
            make 'clean'
        }

        stage('Lint') {
            lintDockerfile()
        }

        new Docker(this)
                .image("golang:${goVersion}")
                .mountJenkinsUser()
                .inside("--volume ${WORKSPACE}:/go/src/${project} -w /go/src/${project}") {
                    stage('Build') {
                        make 'build-controller'
                    }

                    stage('Unit test') {
                        make 'unit-test'
                        junit allowEmptyResults: true, testResults: 'target/unit-tests/*-tests.xml'
                    }

                    stage('Review dog analysis') {
                        stageStaticAnalysisReviewDog()
                    }

                    stage('Generate k8s Resources') {
                        make 'helm-generate'
                        archiveArtifacts "${helmTargetDir}/**/*"
                    }

                    stage('Lint helm') {
                        make 'helm-lint'
                    }
                }

        stage('SonarQube') {
            stageStaticAnalysisSonarQube()
        }

        stageSmokeTest()

        stageAutomaticRelease(makefile)
    }
}

void gitWithCredentials(String command) {
    withCredentials([usernamePassword(credentialsId: 'cesmarvin', usernameVariable: 'GIT_AUTH_USR', passwordVariable: 'GIT_AUTH_PSW')]) {
        sh(
                script: "git -c credential.helper=\"!f() { echo username='\$GIT_AUTH_USR'; echo password='\$GIT_AUTH_PSW'; }; f\" " + command,
                returnStdout: true
        )
    }
}

void stageStaticAnalysisReviewDog() {
    def commitSha = sh(returnStdout: true, script: 'git rev-parse HEAD').trim()

    withCredentials([[$class: 'UsernamePasswordMultiBinding', credentialsId: 'sonarqube-gh', usernameVariable: 'USERNAME', passwordVariable: 'REVIEWDOG_GITHUB_API_TOKEN']]) {
        withEnv(["CI_PULL_REQUEST=${env.CHANGE_ID}", "CI_COMMIT=${commitSha}", "CI_REPO_OWNER=${repositoryOwner}", "CI_REPO_NAME=${repositoryName}"]) {
            make 'static-analysis-ci'
        }
    }
}

void stageStaticAnalysisSonarQube() {
    def scannerHome = tool name: 'sonar-scanner', type: 'hudson.plugins.sonar.SonarRunnerInstallation'
    withSonarQubeEnv {
        sh "git config 'remote.origin.fetch' '+refs/heads/*:refs/remotes/origin/*'"
        gitWithCredentials("fetch --all")

        if (currentBranch == productionReleaseBranch) {
            sh "${scannerHome}/bin/sonar-scanner -Dsonar.branch.name=${env.BRANCH_NAME}"
        } else if (currentBranch == developmentBranch) {
            sh "${scannerHome}/bin/sonar-scanner -Dsonar.branch.name=${env.BRANCH_NAME}"
        } else if (env.CHANGE_TARGET) {
            sh "${scannerHome}/bin/sonar-scanner -Dsonar.pullrequest.key=${env.CHANGE_ID} -Dsonar.pullrequest.branch=${env.CHANGE_BRANCH} -Dsonar.pullrequest.base=${developmentBranch}"
        } else {
            sh "${scannerHome}/bin/sonar-scanner -Dsonar.branch.name=${env.BRANCH_NAME}"
        }
    }
    timeout(time: 2, unit: 'MINUTES') {
        def qGate = waitForQualityGate()
        if (qGate.status != 'OK') {
            unstable("Pipeline unstable due to SonarQube quality gate failure")
        }
    }
}

void stageSmokeTest() {
    K3d k3d = new K3d(this, "${WORKSPACE}", "${WORKSPACE}/k3d", env.PATH)

    try {
        String controllerVersion = makefile.getVersion()

        stage('Set up k3d cluster') {
            k3d.startK3d()
        }

        def imageName
        String namespace = "cloudogu"
        stage('Build & Push Image for Smoke Test') {
            imageName = k3d.buildAndPushToLocalRegistry("${namespace}/${repositoryName}", controllerVersion)
        }

        stage('Update smoke test resources') {
            def repository = imageName.substring(0, imageName.lastIndexOf(":"))
            docker.image("golang:${goVersion}")
                    .mountJenkinsUser()
                    .inside("--volume ${WORKSPACE}:/workdir -w /workdir") {
                        sh "STAGE=development IMAGE_DEV=${repository} make helm-values-replace-image-repo"
                    }
        }

        stage('Deploy smoke test resources') {
            withCredentials([usernamePassword(credentialsId: 'harborhelmchartpush', usernameVariable: 'HARBOR_USERNAME', passwordVariable: 'HARBOR_PASSWORD')]) {
               try {
                k3d.helm("registry login ${registry} --username '${HARBOR_USERNAME}' --password '${HARBOR_PASSWORD}'")
                k3d.helm("install k8s-serviceaccount-crd oci://${registry}/k8s/k8s-serviceaccount-crd --version 1.0.0")
              } finally {
                k3d.helm("registry logout ${registry}")
              }
            }

            k3d.helm("install ${repositoryName} ${helmChartDir}")
        }

        stage('Wait for smoke test rollout') {
            k3d.kubectl("--namespace default rollout status deployment/${repositoryName}")
            k3d.kubectl("--namespace default wait --for=condition=Ready pods -l app.kubernetes.io/name=${repositoryName} --timeout=120s")
        }
    } catch(Exception e) {
        k3d.collectAndArchiveLogs()
        throw e as java.lang.Throwable
    } finally {
        stage('Remove k3d cluster') {
            k3d.deleteK3d()
        }
    }
}

void stageAutomaticRelease(Makefile makefile) {
    if (gitflow.isReleaseBranch()) {
        String controllerVersion = makefile.getVersion()
        String releaseVersion = "v${controllerVersion}".toString()

        stage('Build & Push Image') {
            withCredentials([usernamePassword(credentialsId: 'cesmarvin',
                    passwordVariable: 'CES_MARVIN_PASSWORD',
                    usernameVariable: 'CES_MARVIN_USERNAME')]) {
                sh "echo \"machine github.com\n" +
                        "login ${CES_MARVIN_USERNAME}\n" +
                        "password ${CES_MARVIN_PASSWORD}\" >> ~/.netrc"
            }
            def dockerImage = docker.build("cloudogu/${repositoryName}:${controllerVersion}")
            sh "rm ~/.netrc"
            docker.withRegistry('https://registry.hub.docker.com/', 'dockerHubCredentials') {
                dockerImage.push("${controllerVersion}")
            }
        }

        stage('Sign after Release') {
            gpg.createSignature()
        }

        stage('Push Helm chart to Harbor') {
            new Docker(this)
                    .image("golang:${goVersion}")
                    .mountJenkinsUser()
                    .inside("--volume ${WORKSPACE}:/go/src/${project} -w /go/src/${project}") {
                        make 'helm-package'
                        archiveArtifacts "${helmTargetDir}/**/*"

                        withCredentials([usernamePassword(credentialsId: 'harborhelmchartpush', usernameVariable: 'HARBOR_USERNAME', passwordVariable: 'HARBOR_PASSWORD')]) {
                            sh ".bin/helm registry login ${registry} --username '${HARBOR_USERNAME}' --password '${HARBOR_PASSWORD}'"
                            sh ".bin/helm push ${helmChartDir}/${repositoryName}-${controllerVersion}.tgz oci://${registry}/${registryNamespace}/"
                        }
                    }
        }

        stage('Finish Release') {
            gitflow.finishRelease(releaseVersion, productionReleaseBranch)
        }

        stage('Add Github-Release') {
            github.createReleaseWithChangelog(releaseVersion, changelog, productionReleaseBranch)
        }
    }
}

void make(String makeArgs) {
    sh "make ${makeArgs}"
}
